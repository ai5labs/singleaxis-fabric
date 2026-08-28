// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package supportbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/bundle"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/installtarget"
	public "github.com/singleaxis/singleaxis-fabric/tools/fabricctl/pkg/lifecycle"
)

func supportFixture(t *testing.T) string {
	t.Helper()
	resource := deployment.Resource{APIVersion: deployment.APIVersion, Kind: deployment.Kind, Metadata: deployment.Metadata{Name: "support-agent"}, Spec: deployment.Spec{AssuranceLevel: "A0", Connection: deployment.Connection{Mode: "sdk", TenantIDFrom: "tenant/example"}, Observe: deployment.Observe{ContentMode: "metadata-only"}}}
	digest, _ := deployment.DigestResource(resource)
	target := installtarget.Resource{APIVersion: installtarget.APIVersion, Kind: installtarget.Kind, Metadata: installtarget.Metadata{Name: "support-target"}, Spec: installtarget.Spec{DeploymentRef: installtarget.DeploymentRef{Name: "support-agent", Digest: digest}, Distribution: installtarget.Distribution{ArtifactRef: "oci://registry.example/fabric", Version: "0.7.1", Digest: "sha256:" + strings.Repeat("1", 64)}, Profile: installtarget.Profile{Name: installtarget.ProfilePermissiveDev, Digest: "sha256:" + strings.Repeat("2", 64)}, Backend: installtarget.Backend{Type: "helm", Helm: installtarget.HelmTarget{Context: "kind-support", Namespace: "fabric-system", ReleaseName: "fabric", CreateNamespace: true}}}}
	built, err := bundle.Build(resource, target, bundle.Generator{Name: "fabricctl", Version: "0.7.1", Commit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, item := range built.Artifacts {
		if err := os.WriteFile(filepath.Join(dir, item.Path), item.Payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestWriteCreatesOnlyAllowlistedLocalArtifacts(t *testing.T) {
	bundleDir := supportFixture(t)
	receipt, err := public.FinalizeReceipt(public.OperationReceipt{OperationID: "operation/1", Operation: "install", Actor: "operator/alice", StartedAt: time.Now().Add(-time.Minute), CompletedAt: time.Now(), BundleDigest: bundle.VerifyDirectory(bundleDir).BundleDigest, PlanDigest: "sha256:" + strings.Repeat("3", 64), TargetDigest: "sha256:" + strings.Repeat("4", 64), ApprovalRef: "interactive/local", Outcome: "succeeded", Recovery: "none-required", Verification: public.VerificationSummary{Status: "unverified"}})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "support")
	manifest, err := Write(Options{BundleDir: bundleDir, Receipt: &receipt, OutputDir: output, Generator: Generator{Version: "0.7.1", Commit: strings.Repeat("a", 40)}, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 || manifest.Uploaded || len(manifest.Excluded) == 0 {
		t.Fatalf("unexpected support bundle: entries=%d manifest=%#v", len(entries), manifest)
	}
	for _, forbidden := range []string{"singleaxis.yaml", "install-target.yaml", "secrets-required.yaml"} {
		if _, err := os.Stat(filepath.Join(output, forbidden)); !os.IsNotExist(err) {
			t.Fatalf("forbidden source artifact included: %s", forbidden)
		}
	}
	if _, err := Write(Options{BundleDir: bundleDir, OutputDir: output}); err == nil {
		t.Fatal("support output was replaced")
	}
}
