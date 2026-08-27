// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

// Package kubernetes implements the first Fabric lifecycle backend. Its public
// contract is owned by pkg/lifecycle; command execution remains internal until
// multiple production backends establish a stable plugin boundary.
package kubernetes

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/artifacts"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/bundle"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/installtarget"
	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
	"gopkg.in/yaml.v3"
)

const maxResolvedArtifactBytes = 64 << 20

type Runner interface {
	Run(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error)
}

type PlanOptions struct {
	BundleDir        string
	ChartPath        string
	ProfilePath      string
	ImageLocksPath   string
	Operation        string
	SourceVersion    string
	RollbackRevision string
	ClusterUID       string
}

type ResolvedPlan struct {
	Plan        public.OperationPlan
	Deployment  deployment.Resource
	Target      installtarget.Resource
	ImageLocks  artifacts.ImageLockFile
	ImageValues []byte
}

// Plan resolves only operator-supplied local artifacts and invokes Helm
// template. It does not contact a cluster, registry, platform, or secret store.
func Plan(ctx context.Context, runner Runner, options PlanOptions) (ResolvedPlan, error) {
	if options.Operation == "" {
		options.Operation = "install"
	}
	if options.Operation != "install" && options.Operation != "upgrade" {
		return ResolvedPlan{}, fmt.Errorf("Kubernetes artifact planning supports install or upgrade")
	}
	if options.Operation == "upgrade" && options.SourceVersion == "" {
		return ResolvedPlan{}, fmt.Errorf("upgrade planning requires the discovered current release version")
	}
	report := bundle.VerifyDirectory(options.BundleDir)
	if report.Status != "pass" {
		return ResolvedPlan{}, fmt.Errorf("bundle verification failed: %s", firstDiagnostic(report))
	}
	parsedDeployment, deploymentDiagnostics, err := deployment.ParseFile(filepath.Join(options.BundleDir, bundle.DeploymentFileName))
	if err != nil || len(deploymentDiagnostics) != 0 || parsedDeployment == nil {
		return ResolvedPlan{}, fmt.Errorf("verified deployment could not be loaded")
	}
	parsedTarget, targetDiagnostics, err := installtarget.ParseFile(filepath.Join(options.BundleDir, bundle.InstallTargetFileName))
	if err != nil || len(targetDiagnostics) != 0 || parsedTarget == nil {
		return ResolvedPlan{}, fmt.Errorf("verified install target could not be loaded")
	}
	target := parsedTarget.Resource
	if target.Spec.Backend.Type != "helm" {
		return ResolvedPlan{}, fmt.Errorf("kubernetes backend requires backend.type helm")
	}

	chartDigest, err := digestRegularFile(options.ChartPath)
	if err != nil {
		return ResolvedPlan{}, fmt.Errorf("chart package could not be safely inspected")
	}
	if chartDigest != target.Spec.Distribution.Digest {
		return ResolvedPlan{}, fmt.Errorf("chart package digest does not match the reviewed target")
	}
	profileDigest, err := digestRegularFile(options.ProfilePath)
	if err != nil {
		return ResolvedPlan{}, fmt.Errorf("profile could not be safely inspected")
	}
	if profileDigest != target.Spec.Profile.Digest {
		return ResolvedPlan{}, fmt.Errorf("profile digest does not match the reviewed target")
	}
	imageLockPayload, imageLockDigest, err := readAndDigestRegularFile(options.ImageLocksPath)
	if err != nil {
		return ResolvedPlan{}, fmt.Errorf("image lock file could not be safely inspected")
	}
	imageLocks, err := artifacts.ParseImageLocks(imageLockPayload, formatForPath(options.ImageLocksPath))
	if err != nil {
		return ResolvedPlan{}, err
	}
	if normalizeVersion(imageLocks.Release) != normalizeVersion(target.Spec.Distribution.Version) {
		return ResolvedPlan{}, fmt.Errorf("image lock release does not match the reviewed distribution version")
	}
	imageValues, err := artifacts.RenderHelmValues(imageLocks)
	if err != nil {
		return ResolvedPlan{}, fmt.Errorf("render immutable image values: %w", err)
	}

	valuesPath := filepath.Join(options.BundleDir, bundle.ValuesFileName)
	manifest, err := runner.Run(ctx, "helm", []string{
		"template", target.Spec.Backend.Helm.ReleaseName, options.ChartPath,
		"--namespace", target.Spec.Backend.Helm.Namespace,
		"--values", options.ProfilePath,
		"--values", valuesPath,
		"--values", "-",
	}, imageValues)
	if err != nil {
		return ResolvedPlan{}, fmt.Errorf("helm render failed without producing a mutation plan")
	}
	effects, images, err := inspectManifest(manifest)
	if err != nil {
		return ResolvedPlan{}, err
	}
	if err := verifyLockedImages(images, imageLocks); err != nil {
		return ResolvedPlan{}, err
	}
	targetDigest, err := installtarget.Digest(target)
	if err != nil {
		return ResolvedPlan{}, fmt.Errorf("target identity could not be computed")
	}
	resolvedArtifacts := []public.ArtifactResolution{
		{Kind: "chart", Reference: target.Spec.Distribution.ArtifactRef, Version: target.Spec.Distribution.Version, Digest: chartDigest},
		{Kind: "profile", Reference: target.Spec.Profile.Name, Digest: profileDigest},
		{Kind: "image-locks", Reference: filepath.Base(options.ImageLocksPath), Version: imageLocks.Release, Digest: imageLockDigest},
	}
	for _, image := range imageLocks.Images {
		resolvedArtifacts = append(resolvedArtifacts, public.ArtifactResolution{
			Kind: "container-image", Reference: image.Component + ":" + image.Repository, Version: imageLocks.Release, Digest: image.Digest,
		})
	}
	approval := "interactive"
	if options.Operation != "install" || parsedDeployment.Resource.Spec.AssuranceLevel == "A2" || parsedDeployment.Resource.Spec.AssuranceLevel == "A3" {
		approval = "required"
	}
	plan, err := public.BuildOperationPlan(public.OperationPlan{
		Operation: options.Operation, Readiness: planReadiness(options.ClusterUID), BundleDigest: report.BundleDigest, TargetDigest: targetDigest,
		SourceVersion: options.SourceVersion, TargetVersion: target.Spec.Distribution.Version,
		Target: public.TargetIdentity{Backend: "kubernetes-helm", Context: target.Spec.Backend.Helm.Context, ClusterUID: options.ClusterUID,
			Namespace: target.Spec.Backend.Helm.Namespace, ReleaseName: target.Spec.Backend.Helm.ReleaseName},
		Artifacts: resolvedArtifacts, Effects: effects, Approval: approval,
		Compatibility: public.PlanCompatibility{RollbackRevision: options.RollbackRevision},
	})
	if err != nil {
		return ResolvedPlan{}, err
	}
	return ResolvedPlan{Plan: plan, Deployment: parsedDeployment.Resource, Target: target, ImageLocks: imageLocks, ImageValues: imageValues}, nil
}

func planReadiness(clusterUID string) string {
	if clusterUID == "" {
		return "draft"
	}
	return "mutation-ready"
}

// DiscoverClusterUID resolves the reviewed context to the immutable UID of the
// kube-system Namespace. It is read-only but contacts the selected cluster.
func DiscoverClusterUID(ctx context.Context, runner Runner, contextName string) (string, error) {
	payload, err := runner.Run(ctx, "kubectl", []string{
		"--context", contextName, "get", "namespace", "kube-system", "--output", "jsonpath={.metadata.uid}",
	}, nil)
	if err != nil {
		return "", fmt.Errorf("selected Kubernetes context could not be resolved")
	}
	uid := strings.TrimSpace(string(payload))
	if uid == "" || len(uid) > 128 || strings.ContainsAny(uid, " \t\r\n/\\") {
		return "", fmt.Errorf("selected Kubernetes context returned an invalid cluster identity")
	}
	return uid, nil
}

func firstDiagnostic(report bundle.Report) string {
	if len(report.Diagnostics) == 0 {
		return "bundle is not internally consistent"
	}
	return report.Diagnostics[0].ID
}

func formatForPath(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return "json"
	}
	return "yaml"
}

func normalizeVersion(value string) string { return strings.TrimPrefix(value, "v") }

func digestRegularFile(path string) (string, error) {
	_, digest, err := readAndDigestRegularFile(path)
	return digest, err
}

func readAndDigestRegularFile(path string) ([]byte, string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxResolvedArtifactBytes {
		return nil, "", fmt.Errorf("artifact must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxResolvedArtifactBytes+1))
	if err != nil || len(payload) > maxResolvedArtifactBytes {
		return nil, "", fmt.Errorf("artifact exceeds the byte limit")
	}
	digest := sha256.Sum256(payload)
	return payload, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func inspectManifest(payload []byte) ([]public.PlannedEffect, []string, error) {
	decoder := yaml.NewDecoder(bufio.NewReader(bytes.NewReader(payload)))
	effects := make([]public.PlannedEffect, 0)
	images := make([]string, 0)
	for {
		var document any
		if err := decoder.Decode(&document); err != nil {
			if err == io.EOF {
				break
			}
			return nil, nil, fmt.Errorf("rendered manifest is malformed")
		}
		object, ok := document.(map[string]any)
		if !ok || len(object) == 0 {
			continue
		}
		apiVersion, _ := object["apiVersion"].(string)
		kind, _ := object["kind"].(string)
		metadata, _ := object["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		namespace, _ := metadata["namespace"].(string)
		if apiVersion == "" || kind == "" || name == "" {
			return nil, nil, fmt.Errorf("rendered manifest contains an unidentified resource")
		}
		if kind == "Secret" {
			return nil, nil, fmt.Errorf("planning refuses to retain or inspect a rendered Secret")
		}
		if isHelmTestHook(metadata) {
			// Helm test hooks are not installed by helm upgrade/install. Their
			// images are resolved separately by runtime verification.
			continue
		}
		identity := apiVersion + "/" + kind + "/"
		if namespace != "" {
			identity += namespace + "/"
		}
		identity += name
		effects = append(effects, public.PlannedEffect{Action: "apply", Resource: identity})
		collectImages(object, &images)
	}
	if len(effects) == 0 {
		return nil, nil, fmt.Errorf("helm render produced no installable resources")
	}
	sort.Strings(images)
	return effects, images, nil
}

func isHelmTestHook(metadata map[string]any) bool {
	annotations, _ := metadata["annotations"].(map[string]any)
	hook, _ := annotations["helm.sh/hook"].(string)
	for _, value := range strings.Split(hook, ",") {
		value = strings.TrimSpace(value)
		if value == "test" || value == "test-success" || value == "test-failure" {
			return true
		}
	}
	return false
}

func collectImages(value any, result *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "image" {
				if image, ok := child.(string); ok {
					*result = append(*result, image)
				}
			}
			collectImages(child, result)
		}
	case []any:
		for _, child := range typed {
			collectImages(child, result)
		}
	}
}

func verifyLockedImages(rendered []string, locks artifacts.ImageLockFile) error {
	if len(rendered) == 0 {
		return fmt.Errorf("rendered workloads contain no inspectable images")
	}
	allowed := make(map[string]bool, len(locks.Images))
	for _, image := range locks.Images {
		allowed[image.Repository+"@"+image.Digest] = true
	}
	seen := make(map[string]bool, len(rendered))
	for _, image := range rendered {
		if !allowed[image] {
			return fmt.Errorf("rendered workload contains an unapproved or mutable image")
		}
		seen[image] = true
	}
	for image := range allowed {
		if !seen[image] {
			return fmt.Errorf("image lock contains a component not present in the rendered workload")
		}
	}
	return nil
}
