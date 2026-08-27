// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
)

const (
	OperationPlanSchema     = "fabricctl.operation-plan/v1"
	OperationReceiptSchema  = "fabricctl.operation-receipt/v1"
	StatusSnapshotSchema    = "fabricctl.status-snapshot/v1"
	RuntimeVerifySchema     = "fabricctl.runtime-verification/v1"
	ConnectionReceiptSchema = "fabricctl.connection-receipt/v1"
)

type ArtifactResolution struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	Version   string `json:"version,omitempty"`
	Digest    string `json:"digest"`
}

type TargetIdentity struct {
	Backend     string `json:"backend"`
	Context     string `json:"context,omitempty"`
	ClusterUID  string `json:"cluster_uid,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	ReleaseName string `json:"release_name"`
}

type PlannedEffect struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
}

type PlanCompatibility struct {
	MinimumCurrentVersion string `json:"minimum_current_version,omitempty"`
	MaximumCurrentVersion string `json:"maximum_current_version,omitempty"`
	RollbackRevision      string `json:"rollback_revision,omitempty"`
}

// OperationPlan is deterministic. It contains no timestamps or credentials.
// PlanDigest is calculated over all other fields in canonical JSON.
type OperationPlan struct {
	SchemaVersion string               `json:"schema_version"`
	Operation     string               `json:"operation"`
	Readiness     string               `json:"readiness"`
	PlanDigest    string               `json:"plan_digest"`
	BundleDigest  string               `json:"bundle_digest"`
	TargetDigest  string               `json:"target_digest"`
	SourceVersion string               `json:"source_version,omitempty"`
	TargetVersion string               `json:"target_version"`
	Target        TargetIdentity       `json:"target"`
	Artifacts     []ArtifactResolution `json:"artifacts"`
	Effects       []PlannedEffect      `json:"effects"`
	Approval      string               `json:"approval"`
	Compatibility PlanCompatibility    `json:"compatibility"`
}

// BuildOperationPlan normalizes collection order, validates immutable pins,
// and calculates the canonical plan identity used by approvals and receipts.
func BuildOperationPlan(plan OperationPlan) (OperationPlan, error) {
	plan.SchemaVersion = OperationPlanSchema
	plan.PlanDigest = ""
	if !validApprovalOperation(plan.Operation) || (plan.Readiness != "draft" && plan.Readiness != "mutation-ready") ||
		(plan.Readiness == "mutation-ready" && plan.Target.ClusterUID == "") || !approvalDigestPattern.MatchString(plan.BundleDigest) ||
		!approvalDigestPattern.MatchString(plan.TargetDigest) || plan.TargetVersion == "" ||
		(plan.Operation != "install" && plan.SourceVersion == "") ||
		plan.Target.Backend == "" || plan.Target.ReleaseName == "" || len(plan.Artifacts) == 0 ||
		(plan.Approval != "required" && plan.Approval != "interactive") {
		return OperationPlan{}, fmt.Errorf("operation plan has invalid identity, target, or approval posture")
	}
	for _, artifact := range plan.Artifacts {
		if artifact.Kind == "" || artifact.Reference == "" || !approvalDigestPattern.MatchString(artifact.Digest) {
			return OperationPlan{}, fmt.Errorf("operation plan contains an unpinned artifact")
		}
	}
	sort.Slice(plan.Artifacts, func(i, j int) bool {
		if plan.Artifacts[i].Kind == plan.Artifacts[j].Kind {
			return plan.Artifacts[i].Reference < plan.Artifacts[j].Reference
		}
		return plan.Artifacts[i].Kind < plan.Artifacts[j].Kind
	})
	sort.Slice(plan.Effects, func(i, j int) bool {
		if plan.Effects[i].Action == plan.Effects[j].Action {
			return plan.Effects[i].Resource < plan.Effects[j].Resource
		}
		return plan.Effects[i].Action < plan.Effects[j].Action
	})
	payload, err := json.Marshal(plan)
	if err != nil {
		return OperationPlan{}, fmt.Errorf("encode operation plan identity: %w", err)
	}
	digest := sha256.Sum256(payload)
	plan.PlanDigest = "sha256:" + hex.EncodeToString(digest[:])
	return plan, nil
}

type VerificationSummary struct {
	Status      string   `json:"status"`
	Coverage    []string `json:"coverage"`
	Limitations []string `json:"limitations"`
}

type OperationReceipt struct {
	SchemaVersion         string              `json:"schema_version"`
	OperationID           string              `json:"operation_id"`
	Operation             string              `json:"operation"`
	Actor                 string              `json:"actor"`
	StartedAt             time.Time           `json:"started_at"`
	CompletedAt           time.Time           `json:"completed_at"`
	BundleDigest          string              `json:"bundle_digest"`
	PlanDigest            string              `json:"plan_digest"`
	TargetDigest          string              `json:"target_digest"`
	ApprovalRef           string              `json:"approval_ref"`
	SourceRevision        string              `json:"source_revision,omitempty"`
	EffectiveRevision     string              `json:"effective_revision,omitempty"`
	EffectiveDigest       string              `json:"effective_digest,omitempty"`
	Outcome               string              `json:"outcome"`
	Recovery              string              `json:"recovery"`
	Verification          VerificationSummary `json:"verification"`
	PreviousReceiptDigest string              `json:"previous_receipt_digest,omitempty"`
	ReceiptDigest         string              `json:"receipt_digest"`
}

// FinalizeReceipt validates and hash-chains a value-free operation receipt.
func FinalizeReceipt(receipt OperationReceipt) (OperationReceipt, error) {
	receipt.SchemaVersion = OperationReceiptSchema
	receipt.ReceiptDigest = ""
	// Keep the canonical receipt shape stable. Nil slices encode as JSON null,
	// which is not valid for the public array fields.
	if receipt.Verification.Coverage == nil {
		receipt.Verification.Coverage = []string{}
	}
	if receipt.Verification.Limitations == nil {
		receipt.Verification.Limitations = []string{}
	}
	if receipt.OperationID == "" || !validApprovalOperation(receipt.Operation) || receipt.Actor == "" ||
		!safeReceiptReference(receipt.Actor) || !safeReceiptReference(receipt.ApprovalRef) ||
		receipt.StartedAt.IsZero() || receipt.CompletedAt.Before(receipt.StartedAt) ||
		!approvalDigestPattern.MatchString(receipt.BundleDigest) || !approvalDigestPattern.MatchString(receipt.PlanDigest) ||
		!approvalDigestPattern.MatchString(receipt.TargetDigest) ||
		(receipt.Outcome != "succeeded" && receipt.Outcome != "failed" && receipt.Outcome != "incomplete") || receipt.Recovery == "" ||
		!validVerificationStatus(receipt.Verification.Status) {
		return OperationReceipt{}, fmt.Errorf("operation receipt is incomplete or malformed")
	}
	if receipt.PreviousReceiptDigest != "" && !approvalDigestPattern.MatchString(receipt.PreviousReceiptDigest) {
		return OperationReceipt{}, fmt.Errorf("operation receipt chain digest is malformed")
	}
	receipt.StartedAt = receipt.StartedAt.UTC()
	receipt.CompletedAt = receipt.CompletedAt.UTC()
	payload, err := json.Marshal(receipt)
	if err != nil {
		return OperationReceipt{}, fmt.Errorf("encode operation receipt identity: %w", err)
	}
	digest := sha256.Sum256(payload)
	receipt.ReceiptDigest = "sha256:" + hex.EncodeToString(digest[:])
	return receipt, nil
}

func safeReceiptReference(value string) bool {
	return len(value) <= 253 && !strings.ContainsAny(value, " \t\r\n") && !deployment.ReferenceLooksSensitive(value)
}

// ParseAndVerifyReceipt rejects unknown/duplicate fields and verifies the
// self-digest before a receipt is used as expected runtime state.
func ParseAndVerifyReceipt(payload []byte) (OperationReceipt, error) {
	value, err := deployment.LoadBytes(payload, "json")
	if err != nil {
		return OperationReceipt{}, fmt.Errorf("operation receipt cannot be safely decoded")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return OperationReceipt{}, fmt.Errorf("operation receipt cannot be normalized")
	}
	var receipt OperationReceipt
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return OperationReceipt{}, fmt.Errorf("operation receipt does not satisfy the strict contract")
	}
	if _, err := decoder.Token(); err != io.EOF || receipt.SchemaVersion != OperationReceiptSchema || !approvalDigestPattern.MatchString(receipt.ReceiptDigest) {
		return OperationReceipt{}, fmt.Errorf("operation receipt is malformed")
	}
	if receipt.Verification.Coverage == nil || receipt.Verification.Limitations == nil || !validVerificationStatus(receipt.Verification.Status) {
		return OperationReceipt{}, fmt.Errorf("operation receipt verification summary is malformed")
	}
	expectedDigest := receipt.ReceiptDigest
	receipt.ReceiptDigest = ""
	canonicalReceipt, err := json.Marshal(receipt)
	if err != nil {
		return OperationReceipt{}, fmt.Errorf("operation receipt identity cannot be computed")
	}
	digest := sha256.Sum256(canonicalReceipt)
	if "sha256:"+hex.EncodeToString(digest[:]) != expectedDigest {
		return OperationReceipt{}, fmt.Errorf("operation receipt digest does not match its contents")
	}
	receipt.ReceiptDigest = expectedDigest
	return receipt, nil
}

func validVerificationStatus(value string) bool {
	switch value {
	case "verified", "partial", "unverified", "failed":
		return true
	default:
		return false
	}
}

type ComponentStatus struct {
	Name     string `json:"name"`
	Ready    bool   `json:"ready"`
	Revision string `json:"revision,omitempty"`
	Detail   string `json:"detail"`
}

type StatusSnapshot struct {
	SchemaVersion   string            `json:"schema_version"`
	ObservedAt      time.Time         `json:"observed_at"`
	Target          TargetIdentity    `json:"target"`
	DesiredDigest   string            `json:"desired_digest"`
	EffectiveDigest string            `json:"effective_digest,omitempty"`
	Drift           string            `json:"drift"`
	Delivery        string            `json:"delivery"`
	Components      []ComponentStatus `json:"components"`
}

type RuntimeVerification struct {
	SchemaVersion string    `json:"schema_version"`
	ObservedAt    time.Time `json:"observed_at"`
	Status        string    `json:"status"`
	SyntheticID   string    `json:"synthetic_id,omitempty"`
	Checks        []Check   `json:"checks"`
	Limitations   []string  `json:"limitations"`
}

type ConnectionReceipt struct {
	SchemaVersion    string    `json:"schema_version"`
	ConnectionID     string    `json:"connection_id"`
	Mode             string    `json:"mode"`
	EndpointOrigin   string    `json:"endpoint_origin,omitempty"`
	TenantRef        string    `json:"tenant_ref,omitempty"`
	WorkloadRef      string    `json:"workload_ref,omitempty"`
	EffectiveDigest  string    `json:"effective_digest"`
	ConnectedAt      time.Time `json:"connected_at"`
	CredentialStored bool      `json:"credential_stored"`
}
