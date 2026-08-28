// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
)

const ApprovalEnvelopeSchema = "fabricctl.approval-envelope/v1"

var (
	approvalReferencePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._:/-]{0,251}[A-Za-z0-9])?$`)
	approvalDigestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ApprovalScope binds authorization to exact desired state, plan, target, and
// operation. An approval can never be reused for a different mutation.
type ApprovalScope struct {
	Operation    string `json:"operation"`
	BundleDigest string `json:"bundle_digest"`
	PlanDigest   string `json:"plan_digest"`
	TargetDigest string `json:"target_digest"`
}

// ApprovalEnvelope is detached from deterministic desired state. Signature is
// unpadded base64url Ed25519 over the canonical signed fields.
type ApprovalEnvelope struct {
	SchemaVersion string        `json:"schema_version"`
	ApprovalID    string        `json:"approval_id"`
	Issuer        string        `json:"issuer"`
	KeyID         string        `json:"key_id"`
	IssuedAt      time.Time     `json:"issued_at"`
	ExpiresAt     time.Time     `json:"expires_at"`
	Scope         ApprovalScope `json:"scope"`
	Signature     string        `json:"signature"`
}

type approvalSignedFields struct {
	SchemaVersion string        `json:"schema_version"`
	ApprovalID    string        `json:"approval_id"`
	Issuer        string        `json:"issuer"`
	KeyID         string        `json:"key_id"`
	IssuedAt      time.Time     `json:"issued_at"`
	ExpiresAt     time.Time     `json:"expires_at"`
	Scope         ApprovalScope `json:"scope"`
}

// ExpectedApproval is local intent. Verification fails if any signed scope
// differs, even when the signature is otherwise valid.
type ExpectedApproval struct {
	Operation    string
	BundleDigest string
	PlanDigest   string
	TargetDigest string
}

type VerifiedApproval struct {
	ApprovalID string
	Issuer     string
	KeyID      string
	IssuedAt   time.Time
	ExpiresAt  time.Time
	Scope      ApprovalScope
}

var (
	ErrApprovalMalformed     = errors.New("approval envelope is malformed")
	ErrApprovalUntrusted     = errors.New("approval signing key is not trusted")
	ErrApprovalSignature     = errors.New("approval signature is invalid")
	ErrApprovalExpired       = errors.New("approval has expired")
	ErrApprovalNotYetValid   = errors.New("approval is not yet valid")
	ErrApprovalScopeMismatch = errors.New("approval scope does not match the requested mutation")
)

// VerifyApproval verifies strict JSON, trust, time bounds, signature, and exact
// scope. Error values are stable and never include envelope contents.
func VerifyApproval(payload []byte, trustedKeys map[string]ed25519.PublicKey, now time.Time, expected ExpectedApproval) (VerifiedApproval, error) {
	var envelope ApprovalEnvelope
	if err := strictApprovalJSON(payload, &envelope); err != nil || validateApprovalEnvelope(envelope) != nil {
		return VerifiedApproval{}, ErrApprovalMalformed
	}
	publicKey, ok := trustedKeys[envelope.KeyID]
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return VerifiedApproval{}, ErrApprovalUntrusted
	}
	signedPayload, err := approvalSigningPayload(envelope)
	if err != nil {
		return VerifiedApproval{}, ErrApprovalMalformed
	}
	signature, err := base64.RawURLEncoding.DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, signedPayload, signature) {
		return VerifiedApproval{}, ErrApprovalSignature
	}
	now = now.UTC()
	if now.Before(envelope.IssuedAt.UTC().Add(-5 * time.Minute)) {
		return VerifiedApproval{}, ErrApprovalNotYetValid
	}
	if !now.Before(envelope.ExpiresAt.UTC()) {
		return VerifiedApproval{}, ErrApprovalExpired
	}
	if envelope.Scope.Operation != expected.Operation || envelope.Scope.BundleDigest != expected.BundleDigest ||
		envelope.Scope.PlanDigest != expected.PlanDigest || envelope.Scope.TargetDigest != expected.TargetDigest {
		return VerifiedApproval{}, ErrApprovalScopeMismatch
	}
	return VerifiedApproval{
		ApprovalID: envelope.ApprovalID, Issuer: envelope.Issuer, KeyID: envelope.KeyID,
		IssuedAt: envelope.IssuedAt, ExpiresAt: envelope.ExpiresAt, Scope: envelope.Scope,
	}, nil
}

func strictApprovalJSON(payload []byte, target any) error {
	value, err := deployment.LoadBytes(payload, "json")
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func validateApprovalEnvelope(value ApprovalEnvelope) error {
	if value.SchemaVersion != ApprovalEnvelopeSchema || !validApprovalReference(value.ApprovalID) ||
		!validApprovalReference(value.Issuer) || !validApprovalReference(value.KeyID) ||
		value.IssuedAt.IsZero() || value.ExpiresAt.IsZero() || !value.ExpiresAt.After(value.IssuedAt) ||
		value.ExpiresAt.Sub(value.IssuedAt) > 24*time.Hour ||
		!validApprovalOperation(value.Scope.Operation) || !approvalDigestPattern.MatchString(value.Scope.BundleDigest) ||
		!approvalDigestPattern.MatchString(value.Scope.PlanDigest) || !approvalDigestPattern.MatchString(value.Scope.TargetDigest) ||
		strings.Contains(value.Signature, "=") || len(value.Signature) > 128 {
		return ErrApprovalMalformed
	}
	return nil
}

func validApprovalReference(value string) bool {
	return approvalReferencePattern.MatchString(value) && !deployment.ReferenceLooksSensitive(value)
}

func validApprovalOperation(value string) bool {
	switch value {
	case "install", "upgrade", "rollback":
		return true
	default:
		return false
	}
}

func approvalSigningPayload(value ApprovalEnvelope) ([]byte, error) {
	return json.Marshal(approvalSignedFields{
		SchemaVersion: value.SchemaVersion, ApprovalID: value.ApprovalID, Issuer: value.Issuer, KeyID: value.KeyID,
		IssuedAt: value.IssuedAt.UTC(), ExpiresAt: value.ExpiresAt.UTC(), Scope: value.Scope,
	})
}
