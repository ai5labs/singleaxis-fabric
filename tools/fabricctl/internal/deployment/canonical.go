// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CanonicalDocument returns compact, lexicographically keyed UTF-8 JSON. It
// retains every declared field and matches the existing Python implementation
// for every valid v1alpha1 resource.
func CanonicalDocument(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// Digest returns the review identity of the complete decoded input.
func Digest(value any) (string, error) {
	canonical, err := CanonicalDocument(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// DigestResource computes the same identity as digesting a decoded contract
// document. The typed struct must first be decoded into generic maps so JSON
// object keys are ordered lexicographically rather than by Go field order.
func DigestResource(resource Resource) (string, error) {
	raw, err := json.Marshal(resource)
	if err != nil {
		return "", err
	}
	document, err := LoadBytes(raw, "json")
	if err != nil {
		return "", err
	}
	return Digest(document)
}
