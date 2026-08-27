// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package lifecycle_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

const deploymentYAML = `apiVersion: fabric.singleaxis.dev/v1alpha1
kind: FabricDeployment
metadata:
  name: edge-agent
spec:
  assuranceLevel: A0
  connection:
    mode: sdk
    tenantIdFrom: tenant/acme-development
  observe:
    contentMode: metadata-only
`

func targetYAML(t *testing.T, digest string) string {
	t.Helper()
	return `apiVersion: fabric.singleaxis.dev/v1alpha1
kind: FabricInstallTarget
metadata:
  name: edge-agent-local
spec:
  deploymentRef:
    name: edge-agent
    digest: ` + digest + `
  distribution:
    artifactRef: oci://ghcr.io/singleaxis/charts/fabric
    version: 0.7.1
    digest: sha256:` + strings.Repeat("1", 64) + `
  profile:
    name: permissive-dev
    digest: sha256:` + strings.Repeat("2", 64) + `
  backend:
    type: helm
    helm:
      context: kind-fabric
      namespace: fabric-system
      releaseName: fabric
      createNamespace: true
`
}

func TestPublicFacadeBuildsAndVerifiesWithoutInternalImports(t *testing.T) {
	// This digest is intentionally obtained through an initial validation
	// diagnostic-free deployment fixture and the canonical CLI fixture value.
	// Pinning it here also detects changes to the cross-consumer identity.
	const deploymentDigest = "sha256:72edd3986a9255396deb39edbeb4d7b2d7b8fa6c12942374d0fa3452c51d006b"
	built, diagnostics, err := lifecycle.BuildBundle(
		[]byte(deploymentYAML), "yaml", []byte(targetYAML(t, deploymentDigest)), "yaml",
		lifecycle.Generator{Name: "fabricctl", Version: "0.7.1", Commit: strings.Repeat("a", 40)},
	)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("BuildBundle() err=%v diagnostics=%#v", err, diagnostics)
	}
	if len(built.Paths()) != 6 || !strings.HasPrefix(built.BundleDigest, "sha256:") {
		t.Fatalf("unexpected bundle: %#v", built)
	}
	dir := t.TempDir()
	for _, artifact := range built.Artifacts {
		if err := os.WriteFile(filepath.Join(dir, artifact.Path), artifact.Payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report := lifecycle.VerifyBundleDirectory(dir)
	if report.Status != "pass" || report.BundleDigest != built.BundleDigest || report.Operation.Network || report.Operation.Mutating {
		t.Fatalf("VerifyBundleDirectory() = %#v", report)
	}
}

func TestPublicFacadeReturnsValueFreeDiagnostics(t *testing.T) {
	_, diagnostics, err := lifecycle.BuildBundle([]byte("password: exposed\n"), "yaml", nil, "yaml", lifecycle.Generator{})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) == 0 || strings.Contains(strings.ToLower(diagnostics[0].Summary), "exposed") {
		t.Fatalf("unsafe diagnostics: %#v", diagnostics)
	}
}
