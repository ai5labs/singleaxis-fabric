// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestParseApprovalTrustStoreFiltersByPurpose(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded := "ed25519:" + base64.RawURLEncoding.EncodeToString(publicKey)
	payload := []byte(`{"schema_version":"fabricctl.trust-store/v1","keys":[` +
		`{"key_id":"approval-2026","public_key":"` + encoded + `","purposes":["lifecycle-approval"]},` +
		`{"key_id":"release-2026","public_key":"` + encoded + `","purposes":["release-artifact"]}]}`)
	keys, err := ParseApprovalTrustStore(payload, "json")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys["approval-2026"] == nil {
		t.Fatalf("unexpected approval keys: %#v", keys)
	}
}

func TestParseApprovalTrustStoreRejectsDuplicateAndPrivateLookingMaterial(t *testing.T) {
	payloads := [][]byte{
		[]byte(`{"schema_version":"fabricctl.trust-store/v1","keys":[{"key_id":"same","public_key":"ed25519:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","purposes":["lifecycle-approval"]},{"key_id":"same","public_key":"ed25519:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","purposes":["lifecycle-approval"]}]}`),
		[]byte(`{"schema_version":"fabricctl.trust-store/v1","keys":[{"key_id":"approval","public_key":"private-key-value","purposes":["lifecycle-approval"]}]}`),
	}
	for _, payload := range payloads {
		if _, err := ParseApprovalTrustStore(payload, "json"); err == nil {
			t.Fatalf("unsafe trust store passed: %s", payload)
		}
	}
}
