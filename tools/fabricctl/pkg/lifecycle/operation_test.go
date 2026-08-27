// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func operationPlanFixture() OperationPlan {
	digest := func(character string) string { return "sha256:" + strings.Repeat(character, 64) }
	return OperationPlan{
		Operation: "install", Readiness: "draft", BundleDigest: digest("1"), TargetDigest: digest("2"), TargetVersion: "0.7.1",
		Target: TargetIdentity{Backend: "kubernetes-helm", Context: "kind-fabric", Namespace: "fabric-system", ReleaseName: "fabric"},
		Artifacts: []ArtifactResolution{
			{Kind: "profile", Reference: "permissive-dev", Digest: digest("4")},
			{Kind: "chart", Reference: "oci://ghcr.io/singleaxis/charts/fabric", Version: "0.7.1", Digest: digest("3")},
		},
		Effects:  []PlannedEffect{{Action: "apply", Resource: "apps/v1/Deployment/fabric-otel-collector"}},
		Approval: "interactive",
	}
}

func TestOperationPlanIsDeterministicAndScopeReady(t *testing.T) {
	first, err := BuildOperationPlan(operationPlanFixture())
	if err != nil {
		t.Fatal(err)
	}
	fixture := operationPlanFixture()
	fixture.Artifacts[0], fixture.Artifacts[1] = fixture.Artifacts[1], fixture.Artifacts[0]
	second, err := BuildOperationPlan(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if first.PlanDigest != second.PlanDigest || !approvalDigestPattern.MatchString(first.PlanDigest) {
		t.Fatalf("non-deterministic plans: %s %s", first.PlanDigest, second.PlanDigest)
	}
}

func TestReceiptIsHashChainedAndRequiresRecoveryPosture(t *testing.T) {
	plan, _ := BuildOperationPlan(operationPlanFixture())
	started := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	receipt, err := FinalizeReceipt(OperationReceipt{
		OperationID: "operation/install-1", Operation: "install", Actor: "workload/spiffe-example",
		StartedAt: started, CompletedAt: started.Add(time.Minute), BundleDigest: plan.BundleDigest,
		PlanDigest: plan.PlanDigest, TargetDigest: plan.TargetDigest, ApprovalRef: "interactive/local",
		Outcome: "succeeded", Recovery: "none-required", Verification: VerificationSummary{Status: "unverified", Limitations: []string{"runtime verification not requested"}},
	})
	if err != nil || !approvalDigestPattern.MatchString(receipt.ReceiptDigest) {
		t.Fatalf("FinalizeReceipt() = %#v, %v", receipt, err)
	}
	receipt.Recovery = ""
	if _, err := FinalizeReceipt(receipt); err == nil {
		t.Fatal("receipt without recovery posture passed")
	}
	receipt.Recovery = "none-required"
	payload, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := ParseAndVerifyReceipt(payload)
	if err != nil || verified.ReceiptDigest != receipt.ReceiptDigest {
		t.Fatalf("ParseAndVerifyReceipt() = %#v, %v", verified, err)
	}
	tampered := bytes.Replace(payload, []byte(`"actor":"workload/spiffe-example"`), []byte(`"actor":"operator/mallory"`), 1)
	if _, err := ParseAndVerifyReceipt(tampered); err == nil {
		t.Fatal("tampered receipt passed")
	}
}

func TestFinalizeReceiptNormalizesVerificationArrays(t *testing.T) {
	started := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	receipt, err := FinalizeReceipt(OperationReceipt{
		OperationID: "operation/install-2", Operation: "install", Actor: "operator/alice",
		StartedAt: started, CompletedAt: started.Add(time.Minute),
		BundleDigest: "sha256:" + strings.Repeat("a", 64),
		PlanDigest:   "sha256:" + strings.Repeat("b", 64),
		TargetDigest: "sha256:" + strings.Repeat("c", 64),
		ApprovalRef:  "interactive/local", Outcome: "succeeded", Recovery: "none-required",
		Verification: VerificationSummary{Status: "unverified"},
	})
	if err != nil {
		t.Fatalf("FinalizeReceipt() error = %v", err)
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if bytes.Contains(payload, []byte(`"coverage":null`)) || bytes.Contains(payload, []byte(`"limitations":null`)) {
		t.Fatalf("receipt contains null verification arrays: %s", payload)
	}
}

func TestReceiptRejectsInvalidVerificationSummary(t *testing.T) {
	started := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	base := OperationReceipt{
		OperationID: "operation/install-3", Operation: "install", Actor: "operator/alice",
		StartedAt: started, CompletedAt: started.Add(time.Minute),
		BundleDigest: "sha256:" + strings.Repeat("a", 64),
		PlanDigest:   "sha256:" + strings.Repeat("b", 64),
		TargetDigest: "sha256:" + strings.Repeat("c", 64),
		ApprovalRef:  "interactive/local", Outcome: "succeeded", Recovery: "none-required",
		Verification: VerificationSummary{Status: "claimed"},
	}
	if _, err := FinalizeReceipt(base); err == nil {
		t.Fatal("receipt with invalid verification status passed")
	}
}
