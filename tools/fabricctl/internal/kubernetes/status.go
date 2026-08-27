// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/bundle"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/installtarget"
	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

// Status reads cluster and Helm state. Delivery remains unverified until a
// destination-specific acknowledgement adapter is available.
func Status(ctx context.Context, runner Runner, bundleDir string, expected *public.OperationReceipt, observedAt time.Time) (public.StatusSnapshot, error) {
	report := bundle.VerifyDirectory(bundleDir)
	if report.Status != "pass" {
		return public.StatusSnapshot{}, fmt.Errorf("bundle verification failed: %s", firstDiagnostic(report))
	}
	parsedTarget, diagnostics, err := installtarget.ParseFile(filepath.Join(bundleDir, bundle.InstallTargetFileName))
	if err != nil || len(diagnostics) != 0 || parsedTarget == nil {
		return public.StatusSnapshot{}, fmt.Errorf("verified install target could not be loaded")
	}
	target := parsedTarget.Resource.Spec.Backend.Helm
	clusterUID, err := DiscoverClusterUID(ctx, runner, target.Context)
	if err != nil {
		return public.StatusSnapshot{}, err
	}
	manifest, err := runner.Run(ctx, "helm", []string{"get", "manifest", target.ReleaseName, "--kube-context", target.Context, "--namespace", target.Namespace}, nil)
	if err != nil {
		return public.StatusSnapshot{}, fmt.Errorf("effective Helm manifest cannot be inspected")
	}
	digest := sha256.Sum256(manifest)
	effectiveDigest := "sha256:" + hex.EncodeToString(digest[:])
	workloads, err := runner.Run(ctx, "kubectl", []string{
		"--context", target.Context, "--namespace", target.Namespace, "get", "deployments,statefulsets,daemonsets",
		"--selector", "app.kubernetes.io/instance=" + target.ReleaseName, "--output", "json",
	}, nil)
	if err != nil {
		return public.StatusSnapshot{}, fmt.Errorf("Fabric workload readiness cannot be inspected")
	}
	components, err := parseWorkloadStatus(workloads)
	if err != nil {
		return public.StatusSnapshot{}, err
	}
	drift := "unknown"
	if expected != nil {
		drift = "detected"
		if expected.EffectiveDigest == effectiveDigest {
			drift = "none-detected"
		}
	}
	return public.StatusSnapshot{
		SchemaVersion: public.StatusSnapshotSchema, ObservedAt: observedAt.UTC(),
		Target:        public.TargetIdentity{Backend: "kubernetes-helm", Context: target.Context, ClusterUID: clusterUID, Namespace: target.Namespace, ReleaseName: target.ReleaseName},
		DesiredDigest: report.BundleDigest, EffectiveDigest: effectiveDigest, Drift: drift, Delivery: "unverified", Components: components,
	}, nil
}

func parseWorkloadStatus(payload []byte) ([]public.ComponentStatus, error) {
	var list struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Replicas int `json:"replicas"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas          int    `json:"readyReplicas"`
				CurrentNumberScheduled int    `json:"currentNumberScheduled"`
				NumberReady            int    `json:"numberReady"`
				CurrentRevision        string `json:"currentRevision"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(payload, &list); err != nil {
		return nil, fmt.Errorf("Kubernetes workload response is malformed")
	}
	components := make([]public.ComponentStatus, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Metadata.Name == "" || item.Kind == "" {
			return nil, fmt.Errorf("Kubernetes workload response contains an unidentified component")
		}
		desired, ready := item.Spec.Replicas, item.Status.ReadyReplicas
		if item.Kind == "DaemonSet" {
			desired, ready = item.Status.CurrentNumberScheduled, item.Status.NumberReady
		}
		components = append(components, public.ComponentStatus{
			Name: item.Kind + "/" + item.Metadata.Name, Ready: desired > 0 && ready == desired,
			Revision: item.Status.CurrentRevision, Detail: fmt.Sprintf("%d/%d ready", ready, desired),
		})
	}
	sort.Slice(components, func(i, j int) bool { return components[i].Name < components[j].Name })
	return components, nil
}
