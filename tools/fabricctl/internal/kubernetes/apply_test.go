// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type applyRunner struct {
	clusterUID string
	installed  bool
	appVersion string
	locked     bool
	mutable    bool
}

func (r *applyRunner) Run(_ context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case name == "kubectl" && strings.Contains(joined, "namespace kube-system"):
		return []byte(r.clusterUID), nil
	case name == "kubectl" && strings.Contains(joined, "get namespace"):
		return []byte(`{"metadata":{"labels":{"app.kubernetes.io/managed-by":"Helm"},"annotations":{"meta.helm.sh/release-name":"fabric","meta.helm.sh/release-namespace":"fabric-system"}}}`), nil
	case name == "kubectl" && strings.Contains(joined, "create --filename -"):
		if r.locked {
			return nil, fmt.Errorf("exists")
		}
		r.locked = true
		return []byte("lease.created"), nil
	case name == "kubectl" && strings.Contains(joined, "delete lease"):
		r.locked = false
		return []byte("lease.deleted"), nil
	case name == "helm" && strings.HasPrefix(joined, "list "):
		if r.installed {
			return []byte(`[{"name":"fabric","revision":"1","app_version":"` + r.appVersion + `"}]`), nil
		}
		return []byte(`[]`), nil
	case name == "helm" && strings.HasPrefix(joined, "upgrade "):
		if !r.locked || !strings.Contains(string(stdin), "digest: sha256:") {
			return nil, fmt.Errorf("unsafe apply")
		}
		r.installed = true
		r.mutable = true
		return []byte("installed"), nil
	case name == "helm" && strings.HasPrefix(joined, "get manifest"):
		return []byte("immutable effective manifest"), nil
	case name == "helm" && strings.HasPrefix(joined, "history "):
		return []byte(`[{"revision":1}]`), nil
	default:
		return nil, fmt.Errorf("unexpected command: %s %s", name, joined)
	}
}

func TestApplyRevalidatesTargetLocksAndProducesReceipt(t *testing.T) {
	options, collectorDigest, nemoDigest := writePlanFixture(t)
	planRunner := &fakeRunner{manifest: []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: fabric-otel}\nspec: {template: {spec: {containers: [{name: otel, image: ghcr.io/singleaxis/fabric-otelcol@" + collectorDigest + "}, {name: nemo, image: ghcr.io/singleaxis/fabric-nemo-sidecar@" + nemoDigest + "}]}}}\n")}
	options.ClusterUID = "cluster-uid-1"
	resolved, err := Plan(context.Background(), planRunner, options)
	if err != nil {
		t.Fatal(err)
	}
	runner := &applyRunner{clusterUID: "cluster-uid-1"}
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	result := Apply(context.Background(), runner, resolved, ApplyOptions{
		ChartPath: options.ChartPath, ProfilePath: options.ProfilePath, BundleDir: options.BundleDir,
		Actor: "operator/alice", ApprovalRef: "interactive/local", Now: func() time.Time { now = now.Add(time.Second); return now },
	})
	if result.Err != nil || result.Receipt.Outcome != "succeeded" || result.Receipt.EffectiveRevision != "helm-revision/1" || runner.locked {
		t.Fatalf("Apply() = %#v runner=%#v", result, runner)
	}
}

func TestApplyRejectsChangedClusterAndExistingRelease(t *testing.T) {
	options, collectorDigest, nemoDigest := writePlanFixture(t)
	planRunner := &fakeRunner{manifest: []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: fabric}\nspec: {template: {spec: {containers: [{name: otel, image: ghcr.io/singleaxis/fabric-otelcol@" + collectorDigest + "}, {name: nemo, image: ghcr.io/singleaxis/fabric-nemo-sidecar@" + nemoDigest + "}]}}}\n")}
	options.ClusterUID = "approved-cluster"
	resolved, err := Plan(context.Background(), planRunner, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, runner := range []*applyRunner{{clusterUID: "different"}, {clusterUID: "approved-cluster", installed: true, appVersion: "0.7.0"}} {
		result := Apply(context.Background(), runner, resolved, ApplyOptions{ChartPath: options.ChartPath, ProfilePath: options.ProfilePath, BundleDir: options.BundleDir, Actor: "operator/alice", ApprovalRef: "interactive/local"})
		if result.Err == nil || result.Receipt.Outcome == "succeeded" {
			t.Fatalf("unsafe apply passed: %#v", result)
		}
	}
}

func TestUpgradeRequiresExactDiscoveredSourceVersion(t *testing.T) {
	options, collectorDigest, nemoDigest := writePlanFixture(t)
	options.Operation = "upgrade"
	options.SourceVersion = "0.7.0"
	options.RollbackRevision = "helm-revision/1"
	options.ClusterUID = "approved-cluster"
	planRunner := &fakeRunner{manifest: []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: fabric}\nspec: {template: {spec: {containers: [{name: otel, image: ghcr.io/singleaxis/fabric-otelcol@" + collectorDigest + "}, {name: nemo, image: ghcr.io/singleaxis/fabric-nemo-sidecar@" + nemoDigest + "}]}}}\n")}
	resolved, err := Plan(context.Background(), planRunner, options)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Plan.Approval != "required" || resolved.Plan.SourceVersion != "0.7.0" {
		t.Fatalf("unsafe upgrade plan: %#v", resolved.Plan)
	}
	runner := &applyRunner{clusterUID: "approved-cluster", installed: true, appVersion: "0.7.0"}
	result := Apply(context.Background(), runner, resolved, ApplyOptions{ChartPath: options.ChartPath, ProfilePath: options.ProfilePath, BundleDir: options.BundleDir, Actor: "operator/alice", ApprovalRef: "approval/change-2"})
	if result.Err != nil || result.Receipt.Outcome != "succeeded" {
		t.Fatalf("upgrade failed: %#v", result)
	}
	runner = &applyRunner{clusterUID: "approved-cluster", installed: true, appVersion: "0.6.0"}
	result = Apply(context.Background(), runner, resolved, ApplyOptions{ChartPath: options.ChartPath, ProfilePath: options.ProfilePath, BundleDir: options.BundleDir, Actor: "operator/alice", ApprovalRef: "approval/change-2"})
	if result.Err == nil {
		t.Fatalf("stale source version passed: %#v", result)
	}
}

func TestOwnedNamespaceDocumentIsHelmAdoptableAndRejectsUnownedNamespace(t *testing.T) {
	payload, err := ownedNamespaceDocument("fabric-system", "fabric")
	if err != nil {
		t.Fatal(err)
	}
	if !namespaceOwnedByRelease(payload, "fabric", "fabric-system") {
		t.Fatalf("generated namespace is not Helm-owned: %s", payload)
	}
	if namespaceOwnedByRelease([]byte(`{"metadata":{"name":"fabric-system"}}`), "fabric", "fabric-system") {
		t.Fatal("unowned namespace was accepted")
	}
}
