// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/installtarget"
)

const (
	testDigest    = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testProfile   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testPublicKey = "ed25519:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

func testDeployment(level string) deployment.Resource {
	resource := deployment.Resource{
		APIVersion: deployment.APIVersion,
		Kind:       deployment.Kind,
		Metadata:   deployment.Metadata{Name: "payments-agent"},
		Spec: deployment.Spec{
			AssuranceLevel: level,
			Connection: deployment.Connection{Mode: "gateway", TenantIDFrom: "vault://tenant/payments",
				WorkloadIdentityRef: "spiffe://example.test/payments"},
			Observe: deployment.Observe{ContentMode: "metadata-only"},
		},
	}
	if level == "A3" {
		resource.Spec.Controls = &deployment.Controls{
			ProfileRef: "controls/high-risk", PolicyRef: "policy/payments-v7", AuthorizationRef: "authorization/payments-v5",
			PIIRef: "pii/payments-v3", GuardrailRef: "guardrail/payments-v8", EscalationRef: "escalation/payments-v2",
		}
		resource.Spec.Observe.RelayRef = "relay/payments"
		resource.Spec.Assurance = &deployment.Assurance{PlanRef: "assurance/payments-v4"}
		resource.Spec.Rollout = &deployment.Rollout{ApprovalRef: "approval/change-42"}
	}
	return resource
}

func testTarget(t *testing.T, resource deployment.Resource) installtarget.Resource {
	t.Helper()
	digest, err := deployment.DigestResource(resource)
	if err != nil {
		t.Fatal(err)
	}
	profile := installtarget.ProfilePermissiveDev
	var bindings *installtarget.Bindings
	if resource.Spec.AssuranceLevel == "A3" {
		profile = installtarget.ProfileHighRisk
		bindings = &installtarget.Bindings{
			TenantID: "tenant-payments",
			Exporter: installtarget.Exporter{Endpoint: "https://otlp.example.test/v1/traces", Egress: installtarget.Egress{
				CIDRs: []string{"203.0.113.10/32"}, Ports: []installtarget.Port{{Protocol: "TCP", Port: 443}},
			}},
			UpdateTrust: installtarget.UpdateTrust{KeyID: "singleaxis-release", PublicKey: testPublicKey},
		}
	}
	return installtarget.Resource{
		APIVersion: installtarget.APIVersion,
		Kind:       installtarget.Kind,
		Metadata:   installtarget.Metadata{Name: resource.Metadata.Name},
		Spec: installtarget.Spec{
			DeploymentRef: installtarget.DeploymentRef{Name: resource.Metadata.Name, Digest: digest},
			Distribution:  installtarget.Distribution{ArtifactRef: "oci://ghcr.io/singleaxis/charts/fabric", Version: "0.7.1", Digest: testDigest},
			Profile:       installtarget.Profile{Name: profile, Digest: testProfile},
			Backend: installtarget.Backend{Type: "helm", Helm: installtarget.HelmTarget{
				Context: "payments-cluster", Namespace: "fabric-system", ReleaseName: "fabric", CreateNamespace: true,
			}},
			Bindings: bindings,
		},
	}
}

func TestBuildIsDeterministicAndManifestCoversExactBytes(t *testing.T) {
	resource := testDeployment("A0")
	target := testTarget(t, resource)
	generator := Generator{Name: "fabricctl", Version: "0.7.1", Commit: strings.Repeat("a", 40)}
	first, err := Build(resource, target, generator)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(resource, target, generator)
	if err != nil {
		t.Fatal(err)
	}
	if first.BundleDigest != second.BundleDigest || len(first.Artifacts) != 6 || len(second.Artifacts) != 6 {
		t.Fatalf("bundle identity/order is not deterministic: %#v %#v", first, second)
	}
	for index := range first.Artifacts {
		if first.Artifacts[index].Path != second.Artifacts[index].Path || !bytes.Equal(first.Artifacts[index].Payload, second.Artifacts[index].Payload) {
			t.Fatalf("artifact %d changed across identical builds", index)
		}
	}

	manifestPayload, err := first.Payload(ManifestFileName)
	if err != nil {
		t.Fatal(err)
	}
	var decoded manifest
	if err := json.Unmarshal(manifestPayload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SelfExclusion != ManifestFileName || len(decoded.Files) != 5 || decoded.BundleDigest != first.BundleDigest {
		t.Fatalf("manifest identity = %#v", decoded)
	}
	for _, entry := range decoded.Files {
		payload, err := first.Payload(entry.Path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(payload)
		if entry.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("manifest digest mismatch for %s", entry.Path)
		}
	}
	if recomputed, err := digestManifestEntries(decoded.Files); err != nil || recomputed != first.BundleDigest {
		t.Fatalf("bundle digest = %q, recomputed=%q err=%v", first.BundleDigest, recomputed, err)
	}
}

func TestDevValuesDoNotResolveOpaqueDeploymentReferences(t *testing.T) {
	resource := testDeployment("A0")
	built, err := Build(resource, testTarget(t, resource), Generator{Name: "fabricctl", Version: "0.7.1", Commit: strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	values, _ := built.Payload(ValuesFileName)
	for _, forbidden := range []string{"vault://tenant/payments", "spiffe://example.test/payments", "controls/", "relay/", "policy/"} {
		if bytes.Contains(values, []byte(forbidden)) {
			t.Fatalf("derived Helm values resolved opaque deployment reference %q:\n%s", forbidden, values)
		}
	}
	planPayload, _ := built.Payload(InstallationPlanFileName)
	if !bytes.Contains(planPayload, []byte(`"readiness": "unverified"`)) || !bytes.Contains(planPayload, []byte(`"network": false`)) {
		t.Fatalf("plan does not state offline unverified posture:\n%s", planPayload)
	}
}

func TestHighRiskBundleContainsOnlyDeclaredSecretMetadata(t *testing.T) {
	resource := testDeployment("A3")
	built, err := Build(resource, testTarget(t, resource), Generator{Name: "fabricctl", Version: "0.7.1", Commit: strings.Repeat("c", 40)})
	if err != nil {
		t.Fatal(err)
	}
	values, _ := built.Payload(ValuesFileName)
	for _, required := range []string{"fabric-otel-receiver-tls", "fabric-otel-export-auth", "fabric-presidio-tenant-key", "203.0.113.10/32"} {
		if !bytes.Contains(values, []byte(required)) {
			t.Errorf("high-risk values omit %q:\n%s", required, values)
		}
	}
	for _, opaque := range []string{"controls/high-risk", "policy/payments-v7", "relay/payments", "approval/change-42"} {
		if bytes.Contains(values, []byte(opaque)) {
			t.Fatalf("high-risk values leaked opaque reference %q", opaque)
		}
	}
	plan, _ := built.Payload(InstallationPlanFileName)
	for _, obligation := range []string{
		"controls/high-risk", "policy/payments-v7", "relay/payments", "approval/change-42",
		"authorization/payments-v5", "pii/payments-v3", "guardrail/payments-v8", "escalation/payments-v2",
		"assurance/payments-v4", "spiffe://example.test/payments",
		"deployment.prerequisite.a3.separation_of_duties", "deployment.prerequisite.a3.recovery_evidence",
	} {
		if !bytes.Contains(plan, []byte(obligation)) {
			t.Errorf("high-risk plan omits deployment obligation %q:\n%s", obligation, plan)
		}
	}
	secrets, _ := built.Payload(SecretsRequiredFileName)
	if bytes.Contains(secrets, []byte("\ndata:")) || bytes.Contains(secrets, []byte("\nstringData:")) {
		t.Fatalf("secret requirements contain value-bearing Kubernetes fields:\n%s", secrets)
	}
	if bytes.Count(secrets, []byte("status: unresolved")) != 1 {
		t.Fatalf("secret requirements do not declare one resource-level unresolved state:\n%s", secrets)
	}
}

func TestBuildRejectsStaleTargetBinding(t *testing.T) {
	resource := testDeployment("A0")
	target := testTarget(t, resource)
	target.Spec.DeploymentRef.Digest = testDigest
	_, err := Build(resource, target, Generator{Name: "fabricctl", Version: "0.7.1", Commit: strings.Repeat("d", 40)})
	if err == nil || strings.Contains(err.Error(), target.Spec.DeploymentRef.Digest) {
		t.Fatalf("Build() stale target error = %v", err)
	}
}
