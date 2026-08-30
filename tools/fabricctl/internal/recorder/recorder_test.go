// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package recorder

import (
	"strings"
	"testing"
)

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func validRecorder() Resource {
	return Resource{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   Metadata{Name: "healthcare-shadow"},
		Spec: Spec{
			Identity: Identity{RecorderID: "healthcare-shadow", SystemID: "system/ambient-ai", DeploymentID: "deployment/production-v1"},
			Input:    Input{Method: "otlp"}, Content: Content{Mode: "metadata"},
			Protect:      Protect{PrivacyPolicyRef: "privacy/metadata-only-v1", ConfigDigest: testDigest},
			Destination:  Reference{Ref: "destination/customer-monitoring"},
			Installation: Reference{Ref: "installation/hospital-a"},
		},
	}
}

func TestRenderParseAndDigestAreDeterministic(t *testing.T) {
	resource := validRecorder()
	payload, err := Render(resource)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(payload)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Digest(resource)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Digest(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("digests = %q, %q", first, second)
	}
}

func TestParseRejectsUnknownManagementConcepts(t *testing.T) {
	payload, err := Render(validRecorder())
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, []byte("  assuranceLevel: A3\n")...)
	if _, err := Parse(payload); err == nil {
		t.Fatal("unknown assurance field was accepted")
	}
}

func TestValidateRejectsUnsafeOrIncompleteReferences(t *testing.T) {
	for name, mutate := range map[string]func(*Resource){
		"identity mismatch":       func(r *Resource) { r.Spec.Identity.RecorderID = "another-recorder" },
		"secret-like destination": func(r *Resource) { r.Spec.Destination.Ref = "gh" + "p_123456789012345678901234567890123456" },
		"bad digest":              func(r *Resource) { r.Spec.Protect.ConfigDigest = "latest" },
		"unknown input":           func(r *Resource) { r.Spec.Input.Method = "ebpf" },
	} {
		t.Run(name, func(t *testing.T) {
			resource := validRecorder()
			mutate(&resource)
			if err := Validate(resource); err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
		})
	}
}
