// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/bundle"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/installtarget"
)

type fakeRunner struct {
	manifest []byte
	args     []string
	stdin    []byte
}

func (r *fakeRunner) Run(_ context.Context, name string, args []string, stdin []byte) ([]byte, error) {
	if name != "helm" {
		return nil, os.ErrInvalid
	}
	r.args = append([]string(nil), args...)
	r.stdin = append([]byte(nil), stdin...)
	return append([]byte(nil), r.manifest...), nil
}

func fileDigest(payload []byte) string {
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func writePlanFixture(t *testing.T) (PlanOptions, string, string) {
	t.Helper()
	dir := t.TempDir()
	chart := []byte("pinned chart archive")
	profile := []byte("profile:\n  name: permissive-dev\n")
	imageDigest1 := "sha256:" + strings.Repeat("1", 64)
	imageDigest2 := "sha256:" + strings.Repeat("2", 64)
	locks := []byte(`{"schema_version":"fabricctl.image-locks/v1","release":"0.7.1","images":[` +
		`{"component":"otel-collector","repository":"ghcr.io/singleaxis/fabric-otelcol","digest":"` + imageDigest1 + `"},` +
		`{"component":"nemo-sidecar","repository":"ghcr.io/singleaxis/fabric-nemo-sidecar","digest":"` + imageDigest2 + `"}]}`)
	resource := deployment.Resource{APIVersion: deployment.APIVersion, Kind: deployment.Kind, Metadata: deployment.Metadata{Name: "edge-agent"}, Spec: deployment.Spec{
		AssuranceLevel: "A0", Connection: deployment.Connection{Mode: "sdk", TenantIDFrom: "tenant/example"}, Observe: deployment.Observe{ContentMode: "metadata-only"},
	}}
	deploymentDigest, err := deployment.DigestResource(resource)
	if err != nil {
		t.Fatal(err)
	}
	target := installtarget.Resource{APIVersion: installtarget.APIVersion, Kind: installtarget.Kind, Metadata: installtarget.Metadata{Name: "edge-agent-local"}, Spec: installtarget.Spec{
		DeploymentRef: installtarget.DeploymentRef{Name: "edge-agent", Digest: deploymentDigest},
		Distribution:  installtarget.Distribution{ArtifactRef: "oci://ghcr.io/singleaxis/charts/fabric", Version: "0.7.1", Digest: fileDigest(chart)},
		Profile:       installtarget.Profile{Name: installtarget.ProfilePermissiveDev, Digest: fileDigest(profile)},
		Backend:       installtarget.Backend{Type: "helm", Helm: installtarget.HelmTarget{Context: "kind-fabric", Namespace: "fabric-system", ReleaseName: "fabric", CreateNamespace: true}},
	}}
	built, err := bundle.Build(resource, target, bundle.Generator{Name: "fabricctl", Version: "0.7.1", Commit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range built.Artifacts {
		if err := os.WriteFile(filepath.Join(dir, artifact.Path), artifact.Payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	chartPath := filepath.Join(t.TempDir(), "fabric-0.7.1.tgz")
	profilePath := filepath.Join(t.TempDir(), "permissive-dev.yaml")
	locksPath := filepath.Join(t.TempDir(), "images.lock.json")
	for path, payload := range map[string][]byte{chartPath: chart, profilePath: profile, locksPath: locks} {
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return PlanOptions{BundleDir: dir, ChartPath: chartPath, ProfilePath: profilePath, ImageLocksPath: locksPath}, imageDigest1, imageDigest2
}

func TestPlanBindsArtifactsAndOnlyImmutableRenderedImages(t *testing.T) {
	options, collectorDigest, nemoDigest := writePlanFixture(t)
	runner := &fakeRunner{manifest: []byte(`apiVersion: apps/v1
kind: Deployment
metadata: {name: fabric-otel, namespace: fabric-system}
spec:
  template:
    spec:
      containers:
        - name: collector
          image: ghcr.io/singleaxis/fabric-otelcol@` + collectorDigest + `
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: fabric-nemo, namespace: fabric-system}
spec:
  template:
    spec:
      containers:
        - name: nemo
          image: ghcr.io/singleaxis/fabric-nemo-sidecar@` + nemoDigest + `
`)}
	resolved, err := Plan(context.Background(), runner, options)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Plan.Approval != "interactive" || len(resolved.Plan.Artifacts) != 5 || len(resolved.Plan.Effects) != 2 || !strings.HasPrefix(resolved.Plan.PlanDigest, "sha256:") {
		t.Fatalf("unexpected plan: %#v", resolved.Plan)
	}
	if !strings.Contains(string(runner.stdin), "digest: sha256:") {
		t.Fatalf("helm did not receive immutable values: %s", runner.stdin)
	}
}

func TestPlanRejectsMutableOrUnlistedRenderedImages(t *testing.T) {
	options, _, _ := writePlanFixture(t)
	for _, image := range []string{"ghcr.io/singleaxis/fabric-otelcol:latest", "docker.io/curlimages/curl@sha256:" + strings.Repeat("3", 64)} {
		runner := &fakeRunner{manifest: []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: unsafe}\nspec:\n  template:\n    spec:\n      containers:\n        - name: unsafe\n          image: " + image + "\n")}
		if _, err := Plan(context.Background(), runner, options); err == nil {
			t.Fatalf("unsafe image passed: %s", image)
		}
	}
}

func TestManifestInspectionExcludesHelmTestHookImages(t *testing.T) {
	payload := []byte("apiVersion: v1\nkind: Pod\nmetadata:\n  name: fabric-test\n  annotations:\n    helm.sh/hook: test\nspec:\n  containers:\n    - name: curl\n      image: curlimages/curl:latest\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata: {name: fabric}\nspec: {template: {spec: {containers: [{name: fabric, image: registry/fabric@sha256:" + strings.Repeat("1", 64) + "}]}}}\n")
	effects, images, err := inspectManifest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects) != 1 || len(images) != 1 || strings.Contains(images[0], "curl") {
		t.Fatalf("test hook leaked into install plan: effects=%#v images=%#v", effects, images)
	}
}

func TestPlanRejectsArtifactDigestMismatchBeforeHelm(t *testing.T) {
	options, _, _ := writePlanFixture(t)
	if err := os.WriteFile(options.ChartPath, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(context.Background(), &fakeRunner{}, options); err == nil {
		t.Fatal("changed chart passed")
	}
}
