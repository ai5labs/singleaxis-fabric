// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func signedApproval(t *testing.T) ([]byte, ed25519.PublicKey, ApprovalEnvelope, ExpectedApproval) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	digest := func(character string) string { return "sha256:" + strings.Repeat(character, 64) }
	envelope := ApprovalEnvelope{
		SchemaVersion: ApprovalEnvelopeSchema,
		ApprovalID:    "approval/change-1842", Issuer: "singleaxis.example", KeyID: "release-key-2026",
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
		Scope: ApprovalScope{Operation: "install", BundleDigest: digest("1"), PlanDigest: digest("2"), TargetDigest: digest("3")},
	}
	signedPayload, err := approvalSigningPayload(envelope)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, signedPayload))
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	expected := ExpectedApproval{Operation: envelope.Scope.Operation, BundleDigest: envelope.Scope.BundleDigest, PlanDigest: envelope.Scope.PlanDigest, TargetDigest: envelope.Scope.TargetDigest}
	return payload, publicKey, envelope, expected
}

func TestVerifyApprovalPassesExactScope(t *testing.T) {
	payload, publicKey, envelope, expected := signedApproval(t)
	verified, err := VerifyApproval(payload, map[string]ed25519.PublicKey{envelope.KeyID: publicKey}, envelope.IssuedAt.Add(time.Minute), expected)
	if err != nil || verified.ApprovalID != envelope.ApprovalID || verified.Scope != envelope.Scope {
		t.Fatalf("VerifyApproval() = %#v, %v", verified, err)
	}
}

func TestVerifyApprovalRejectsTamperingScopeAndTime(t *testing.T) {
	payload, publicKey, envelope, expected := signedApproval(t)
	trusted := map[string]ed25519.PublicKey{envelope.KeyID: publicKey}

	var changed ApprovalEnvelope
	if err := json.Unmarshal(payload, &changed); err != nil {
		t.Fatal(err)
	}
	changed.Scope.Operation = "rollback"
	tampered, _ := json.Marshal(changed)
	if _, err := VerifyApproval(tampered, trusted, envelope.IssuedAt, expected); !errors.Is(err, ErrApprovalSignature) {
		t.Fatalf("tamper error = %v", err)
	}

	expected.PlanDigest = "sha256:" + strings.Repeat("4", 64)
	if _, err := VerifyApproval(payload, trusted, envelope.IssuedAt, expected); !errors.Is(err, ErrApprovalScopeMismatch) {
		t.Fatalf("scope error = %v", err)
	}
	if _, err := VerifyApproval(payload, trusted, envelope.ExpiresAt, ExpectedApproval{
		Operation: envelope.Scope.Operation, BundleDigest: envelope.Scope.BundleDigest,
		PlanDigest: envelope.Scope.PlanDigest, TargetDigest: envelope.Scope.TargetDigest,
	}); !errors.Is(err, ErrApprovalExpired) {
		t.Fatalf("expiry error = %v", err)
	}
}

func TestVerifyApprovalRejectsUnknownDuplicateAndUntrustedKey(t *testing.T) {
	payload, publicKey, envelope, expected := signedApproval(t)
	unknown := []byte(strings.Replace(string(payload), `"schema_version":`, `"unknown":true,"schema_version":`, 1))
	if _, err := VerifyApproval(unknown, map[string]ed25519.PublicKey{envelope.KeyID: publicKey}, envelope.IssuedAt, expected); !errors.Is(err, ErrApprovalMalformed) {
		t.Fatalf("unknown field error = %v", err)
	}
	duplicate := []byte(strings.Replace(string(payload), `"schema_version":`, `"schema_version":"fabricctl.approval-envelope/v1","schema_version":`, 1))
	if _, err := VerifyApproval(duplicate, map[string]ed25519.PublicKey{envelope.KeyID: publicKey}, envelope.IssuedAt, expected); !errors.Is(err, ErrApprovalMalformed) {
		t.Fatalf("duplicate field error = %v", err)
	}
	if _, err := VerifyApproval(payload, nil, envelope.IssuedAt, expected); !errors.Is(err, ErrApprovalUntrusted) {
		t.Fatalf("untrusted error = %v", err)
	}
}
