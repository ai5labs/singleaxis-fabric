// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
)

const TrustStoreSchema = "fabricctl.trust-store/v1"

type TrustStore struct {
	SchemaVersion string     `json:"schema_version"`
	Keys          []TrustKey `json:"keys"`
}

type TrustKey struct {
	KeyID     string   `json:"key_id"`
	PublicKey string   `json:"public_key"`
	Purposes  []string `json:"purposes"`
}

// ParseApprovalTrustStore strictly parses public verification material and
// returns only keys authorized for lifecycle approvals.
func ParseApprovalTrustStore(payload []byte, format string) (map[string]ed25519.PublicKey, error) {
	return ParseTrustStore(payload, format, "lifecycle-approval")
}

// ParseTrustStore returns keys authorized for one exact purpose. Purpose is
// part of local policy; a release key cannot silently become an approval or
// management-receipt key.
func ParseTrustStore(payload []byte, format, requiredPurpose string) (map[string]ed25519.PublicKey, error) {
	if requiredPurpose != "lifecycle-approval" && requiredPurpose != "release-artifact" && requiredPurpose != "management-receipt" {
		return nil, fmt.Errorf("trust store purpose is unsupported")
	}
	document, err := deployment.LoadBytes(payload, format)
	if err != nil {
		return nil, fmt.Errorf("trust store cannot be safely decoded")
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("trust store cannot be normalized")
	}
	var store TrustStore
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store); err != nil {
		return nil, fmt.Errorf("trust store does not satisfy the strict contract")
	}
	if _, err := decoder.Token(); err != io.EOF || store.SchemaVersion != TrustStoreSchema || len(store.Keys) == 0 || len(store.Keys) > 32 {
		return nil, fmt.Errorf("trust store is malformed")
	}
	result := make(map[string]ed25519.PublicKey)
	seen := make(map[string]bool)
	for _, key := range store.Keys {
		if !validApprovalReference(key.KeyID) || seen[key.KeyID] || len(key.Purposes) == 0 {
			return nil, fmt.Errorf("trust store contains an invalid or duplicate key")
		}
		seen[key.KeyID] = true
		purposeAllowed := false
		for _, purpose := range key.Purposes {
			if purpose != "lifecycle-approval" && purpose != "release-artifact" && purpose != "management-receipt" {
				return nil, fmt.Errorf("trust store contains an unsupported key purpose")
			}
			purposeAllowed = purposeAllowed || purpose == requiredPurpose
		}
		encoded, ok := bytes.CutPrefix([]byte(key.PublicKey), []byte("ed25519:"))
		decoded, decodeErr := base64.RawURLEncoding.Strict().DecodeString(string(encoded))
		if !ok || decodeErr != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("trust store contains invalid public verification material")
		}
		if purposeAllowed {
			result[key.KeyID] = ed25519.PublicKey(append([]byte(nil), decoded...))
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("trust store authorizes no key for the required purpose")
	}
	return result, nil
}
