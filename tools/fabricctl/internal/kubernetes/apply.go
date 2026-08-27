// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

type ApplyOptions struct {
	ChartPath   string
	ProfilePath string
	BundleDir   string
	Actor       string
	ApprovalRef string
	Timeout     time.Duration
	Now         func() time.Time
}

type ApplyResult struct {
	Receipt public.OperationReceipt
	Err     error
}

// Apply installs one mutation-ready plan. It re-resolves cluster identity,
// acquires a target-scoped Kubernetes Lease, refuses an existing release, and
// uses Helm atomic installation. It returns a receipt even when mutation fails.
func Apply(ctx context.Context, runner Runner, resolved ResolvedPlan, options ApplyOptions) ApplyResult {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Timeout <= 0 {
		options.Timeout = 10 * time.Minute
	}
	started := options.Now().UTC()
	operationID, err := newOperationID()
	if err != nil {
		return ApplyResult{Err: fmt.Errorf("operation identity could not be generated")}
	}
	result := ApplyResult{}
	finalize := func(outcome, recovery, effectiveRevision, effectiveDigest string, operationErr error) ApplyResult {
		receipt, receiptErr := public.FinalizeReceipt(public.OperationReceipt{
			OperationID: operationID, Operation: resolved.Plan.Operation, Actor: options.Actor,
			StartedAt: started, CompletedAt: options.Now().UTC(), BundleDigest: resolved.Plan.BundleDigest,
			PlanDigest: resolved.Plan.PlanDigest, TargetDigest: resolved.Plan.TargetDigest, ApprovalRef: options.ApprovalRef,
			SourceRevision: resolved.Plan.SourceVersion, EffectiveRevision: effectiveRevision, EffectiveDigest: effectiveDigest,
			Outcome: outcome, Recovery: recovery,
			Verification: public.VerificationSummary{Status: "unverified", Limitations: []string{"run fabricctl verify to establish runtime coverage"}},
		})
		if receiptErr != nil {
			return ApplyResult{Err: fmt.Errorf("operation receipt could not be finalized")}
		}
		return ApplyResult{Receipt: receipt, Err: operationErr}
	}
	if resolved.Plan.Readiness != "mutation-ready" || resolved.Plan.Target.ClusterUID == "" || options.Actor == "" || options.ApprovalRef == "" {
		return finalize("failed", "create a mutation-ready plan and provide actor and approval identity", "", "", fmt.Errorf("mutation prerequisites are incomplete"))
	}
	clusterUID, err := DiscoverClusterUID(ctx, runner, resolved.Plan.Target.Context)
	if err != nil || clusterUID != resolved.Plan.Target.ClusterUID {
		return finalize("failed", "re-plan against the reviewed Kubernetes cluster identity", "", "", fmt.Errorf("target cluster identity changed or cannot be verified"))
	}
	target := resolved.Target.Spec.Backend.Helm
	namespaceResult, err := runner.Run(ctx, "kubectl", []string{"--context", target.Context, "get", "namespace", target.Namespace, "--ignore-not-found", "--output", "json"}, nil)
	if err != nil {
		return finalize("failed", "restore authorized read access to the target namespace", "", "", fmt.Errorf("target namespace cannot be inspected"))
	}
	if strings.TrimSpace(string(namespaceResult)) == "" {
		if !target.CreateNamespace {
			return finalize("failed", "provision the reviewed namespace before retrying", "", "", fmt.Errorf("target namespace does not exist"))
		}
		namespacePayload, documentErr := ownedNamespaceDocument(target.Namespace, target.ReleaseName)
		if documentErr != nil {
			return finalize("failed", "repair reviewed namespace identity before retrying", "", "", fmt.Errorf("target namespace ownership could not be generated"))
		}
		if _, err := runner.Run(ctx, "kubectl", []string{"--context", target.Context, "create", "--filename", "-"}, namespacePayload); err != nil {
			return finalize("incomplete", "inspect namespace creation and retry with the same plan digest", "", "", fmt.Errorf("target namespace could not be created"))
		}
	} else if target.CreateNamespace && !namespaceOwnedByRelease(namespaceResult, target.ReleaseName, target.Namespace) {
		return finalize("failed", "do not adopt an existing namespace implicitly; use a non-owning target or explicitly repair reviewed Helm ownership", "", "", fmt.Errorf("existing target namespace is not owned by the reviewed Helm release"))
	}
	lockName := leaseName(target.ReleaseName)
	leasePayload, err := leaseDocument(lockName, target.Namespace, operationID, started)
	if err != nil {
		return finalize("failed", "retry after local lease generation succeeds", "", "", fmt.Errorf("operation lock could not be generated"))
	}
	if _, err := runner.Run(ctx, "kubectl", []string{"--context", target.Context, "create", "--filename", "-"}, leasePayload); err != nil {
		return finalize("failed", "inspect the existing Fabric operation Lease; do not delete an active lock", "", "", fmt.Errorf("target is locked by another or interrupted operation"))
	}
	lockHeld := true
	releaseLock := func() error {
		if !lockHeld {
			return nil
		}
		_, deleteErr := runner.Run(ctx, "kubectl", []string{"--context", target.Context, "delete", "lease", lockName, "--namespace", target.Namespace, "--ignore-not-found=true"}, nil)
		lockHeld = false
		return deleteErr
	}
	defer releaseLock()

	listed, err := runner.Run(ctx, "helm", []string{"list", "--kube-context", target.Context, "--namespace", target.Namespace, "--filter", "^" + regexp.QuoteMeta(target.ReleaseName) + "$", "--output", "json"}, nil)
	if err != nil {
		_ = releaseLock()
		return finalize("failed", "restore authorized Helm discovery and retry", "", "", fmt.Errorf("existing release state cannot be inspected"))
	}
	releases, parseErr := parseReleases(listed)
	if parseErr != nil || (resolved.Plan.Operation == "install" && len(releases) != 0) ||
		(resolved.Plan.Operation == "upgrade" && (len(releases) != 1 || normalizeVersion(releases[0].AppVersion) != normalizeVersion(resolved.Plan.SourceVersion))) {
		_ = releaseLock()
		return finalize("failed", "use the operation matching current Helm state and re-plan after any release change", "", "", fmt.Errorf("current release state does not match the approved plan"))
	}

	helmArgs := []string{"upgrade"}
	if resolved.Plan.Operation == "install" {
		helmArgs = append(helmArgs, "--install")
	}
	helmArgs = append(helmArgs,
		target.ReleaseName, options.ChartPath,
		"--kube-context", target.Context, "--namespace", target.Namespace,
		"--values", options.ProfilePath, "--values", options.BundleDir+"/fabric-values.yaml", "--values", "-",
		"--atomic", "--wait", "--timeout", options.Timeout.String(), "--history-max", "10",
	)
	if _, err := runner.Run(ctx, "helm", helmArgs, resolved.ImageValues); err != nil {
		lockErr := releaseLock()
		recovery := "Helm atomic recovery was requested; inspect release and receipt before retrying the same plan"
		if lockErr != nil {
			recovery += "; the operation Lease also requires inspection"
		}
		return finalize("incomplete", recovery, "", "", fmt.Errorf("Helm installation did not complete"))
	}
	manifest, err := runner.Run(ctx, "helm", []string{"get", "manifest", target.ReleaseName, "--kube-context", target.Context, "--namespace", target.Namespace}, nil)
	if err != nil {
		_ = releaseLock()
		return finalize("incomplete", "installation completed but effective manifest identity must be recovered with status", target.ReleaseName, "", fmt.Errorf("effective manifest could not be inspected"))
	}
	effectiveDigest := sha256.Sum256(manifest)
	revision := target.ReleaseName + "@" + resolved.Plan.TargetVersion
	if history, historyErr := runner.Run(ctx, "helm", []string{"history", target.ReleaseName, "--kube-context", target.Context, "--namespace", target.Namespace, "--max", "1", "--output", "json"}, nil); historyErr == nil {
		if parsed := latestRevision(history); parsed != "" {
			revision = parsed
		}
	}
	if err := releaseLock(); err != nil {
		return finalize("incomplete", "installation succeeded; inspect and remove only the matching stale operation Lease", revision, "sha256:"+hex.EncodeToString(effectiveDigest[:]), fmt.Errorf("installation lock could not be released"))
	}
	result = finalize("succeeded", "none-required", revision, "sha256:"+hex.EncodeToString(effectiveDigest[:]), nil)
	return result
}

func newOperationID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "operation/" + hex.EncodeToString(buffer), nil
}

func leaseName(releaseName string) string {
	name := "fabricctl-" + releaseName + "-operation"
	if len(name) <= 63 {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	return name[:50] + "-" + hex.EncodeToString(digest[:6])
}

func leaseDocument(name, namespace, holder string, acquired time.Time) ([]byte, error) {
	return json.Marshal(map[string]any{
		"apiVersion": "coordination.k8s.io/v1", "kind": "Lease",
		"metadata": map[string]any{"name": name, "namespace": namespace, "labels": map[string]string{"app.kubernetes.io/managed-by": "fabricctl"}},
		"spec":     map[string]any{"holderIdentity": holder, "acquireTime": acquired.UTC().Format(time.RFC3339Nano), "renewTime": acquired.UTC().Format(time.RFC3339Nano), "leaseDurationSeconds": 900},
	})
}

func ownedNamespaceDocument(namespace, releaseName string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Namespace",
		"metadata": map[string]any{
			"name":        namespace,
			"labels":      map[string]string{"app.kubernetes.io/managed-by": "Helm"},
			"annotations": map[string]string{"meta.helm.sh/release-name": releaseName, "meta.helm.sh/release-namespace": namespace},
		},
	})
}

func namespaceOwnedByRelease(payload []byte, releaseName, releaseNamespace string) bool {
	var namespace struct {
		Metadata struct {
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(payload, &namespace); err != nil {
		return false
	}
	return namespace.Metadata.Labels["app.kubernetes.io/managed-by"] == "Helm" &&
		namespace.Metadata.Annotations["meta.helm.sh/release-name"] == releaseName &&
		namespace.Metadata.Annotations["meta.helm.sh/release-namespace"] == releaseNamespace
}

func latestRevision(payload []byte) string {
	var history []struct {
		Revision any `json:"revision"`
	}
	if err := json.Unmarshal(payload, &history); err != nil || len(history) == 0 {
		return ""
	}
	switch value := history[len(history)-1].Revision.(type) {
	case string:
		if value != "" {
			return "helm-revision/" + value
		}
	case float64:
		if value >= 1 && value == float64(int64(value)) {
			return "helm-revision/" + strconv.FormatInt(int64(value), 10)
		}
	}
	return ""
}

type releaseState struct {
	Name       string `json:"name"`
	Revision   string `json:"revision"`
	AppVersion string `json:"app_version"`
}

func parseReleases(payload []byte) ([]releaseState, error) {
	var releases []releaseState
	if err := json.Unmarshal(payload, &releases); err != nil || len(releases) > 1 {
		return nil, fmt.Errorf("Helm release list is malformed or ambiguous")
	}
	return releases, nil
}

// DiscoverRelease returns the exact current app version and Helm revision.
func DiscoverRelease(ctx context.Context, runner Runner, contextName, namespace, releaseName string) (releaseState, bool, error) {
	payload, err := runner.Run(ctx, "helm", []string{"list", "--kube-context", contextName, "--namespace", namespace, "--filter", "^" + regexp.QuoteMeta(releaseName) + "$", "--output", "json"}, nil)
	if err != nil {
		return releaseState{}, false, fmt.Errorf("current Helm release cannot be discovered")
	}
	releases, err := parseReleases(payload)
	if err != nil {
		return releaseState{}, false, err
	}
	if len(releases) == 0 {
		return releaseState{}, false, nil
	}
	if releases[0].Name != releaseName || releases[0].Revision == "" || releases[0].AppVersion == "" {
		return releaseState{}, false, fmt.Errorf("current Helm release identity is incomplete")
	}
	return releases[0], true, nil
}
