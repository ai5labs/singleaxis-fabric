// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

type ValidationEnvelope struct {
	SchemaVersion string       `json:"schema_version"`
	Status        string       `json:"status"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
}

type PlanEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"`
	Operation     struct {
		Mode     string `json:"mode"`
		Mutating bool   `json:"mutating"`
	} `json:"operation"`
	Plan
}

// ResourceBoundPlanEnvelope binds a descriptive plan to the exact desired
// state from which it was derived. Readiness remains explicitly unverified:
// offline planning does not inspect a runtime, cluster, or platform.
type ResourceBoundPlanEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"`
	Readiness     string `json:"readiness"`
	Operation     struct {
		Mode     string `json:"mode"`
		Mutating bool   `json:"mutating"`
	} `json:"operation"`
	Resource struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Name       string `json:"name"`
		Digest     string `json:"digest"`
	} `json:"resource"`
	Plan
}

type DigestEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"`
	Algorithm     string `json:"algorithm"`
	Digest        string `json:"digest"`
	Resource      struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Name       string `json:"name"`
	} `json:"resource"`
}

func NewValidationEnvelope(diagnostics []Diagnostic) ValidationEnvelope {
	if diagnostics == nil {
		diagnostics = []Diagnostic{}
	}
	status := "pass"
	if len(diagnostics) != 0 {
		status = "fail"
	}
	return ValidationEnvelope{SchemaVersion: "fabricctl.deployment-validation/v1", Status: status, Diagnostics: diagnostics}
}

func NewPlanEnvelope(plan Plan) PlanEnvelope {
	envelope := PlanEnvelope{SchemaVersion: "fabricctl.deployment-plan/v1", Status: "pass", Plan: plan}
	envelope.Operation.Mode = "offline"
	envelope.Operation.Mutating = false
	return envelope
}

// NewResourceBoundPlanEnvelope constructs the v2 plan contract. Callers must
// supply the digest of the complete decoded desired-state document, rather
// than a digest reconstructed from the typed Resource (which may omit future
// fields). NewPlanEnvelope remains available while v1 consumers migrate.
func NewResourceBoundPlanEnvelope(resource Resource, desiredStateDigest string, plan Plan) (ResourceBoundPlanEnvelope, error) {
	if !isCanonicalSHA256Digest(desiredStateDigest) {
		return ResourceBoundPlanEnvelope{}, fmt.Errorf("desired-state digest must be a canonical sha256 digest")
	}
	envelope := ResourceBoundPlanEnvelope{
		SchemaVersion: "fabricctl.deployment-plan/v2",
		Status:        "pass",
		Readiness:     "unverified",
		Plan:          plan,
	}
	envelope.Operation.Mode = "offline"
	envelope.Operation.Mutating = false
	envelope.Resource.APIVersion = resource.APIVersion
	envelope.Resource.Kind = resource.Kind
	envelope.Resource.Name = resource.Metadata.Name
	envelope.Resource.Digest = desiredStateDigest
	return envelope, nil
}

func isCanonicalSHA256Digest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	encoded := value[len("sha256:"):]
	if encoded != strings.ToLower(encoded) {
		return false
	}
	decoded, err := hex.DecodeString(encoded)
	return err == nil && len(decoded) == 32
}

func NewDigestEnvelope(resource Resource, digest string) DigestEnvelope {
	envelope := DigestEnvelope{
		SchemaVersion: "fabricctl.deployment-digest/v1", Status: "pass",
		Algorithm: "sha256", Digest: digest,
	}
	envelope.Resource.APIVersion = resource.APIVersion
	envelope.Resource.Kind = resource.Kind
	envelope.Resource.Name = resource.Metadata.Name
	return envelope
}

// RenderJSON produces deterministic indented JSON with a trailing newline.
func RenderJSON(value any) ([]byte, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

func RenderValidationHuman(diagnostics []Diagnostic) string {
	if len(diagnostics) == 0 {
		return "[PASS] FabricDeployment is valid\n"
	}
	var output strings.Builder
	output.WriteString("[FAIL] FabricDeployment is invalid\n")
	for _, item := range diagnostics {
		fmt.Fprintf(&output, "- [%s] %s: %s\n", item.ID, sanitizeTerminalText(item.Path), item.Summary)
	}
	return output.String()
}

// sanitizeTerminalText renders non-printing runes as inert ASCII escapes.
// Diagnostic paths can contain attacker-controlled mapping keys, so emitting
// them verbatim could inject terminal control sequences into human output.
func sanitizeTerminalText(value string) string {
	var output strings.Builder
	for _, r := range value {
		if unicode.IsPrint(r) {
			output.WriteRune(r)
			continue
		}
		if r <= 0xffff {
			fmt.Fprintf(&output, `\u%04X`, r)
		} else {
			fmt.Fprintf(&output, `\U%08X`, r)
		}
	}
	return output.String()
}

func RenderDigestHuman(resource Resource, digest string) string {
	return fmt.Sprintf("FabricDeployment: %s\nDigest: %s\nAlgorithm: sha256\n", resource.Metadata.Name, digest)
}

func RenderPlanHuman(resource Resource, plan Plan) string {
	var output strings.Builder
	fmt.Fprintf(&output, "FabricDeployment plan: %s\n", resource.Metadata.Name)
	fmt.Fprintf(&output, "Assurance level: %s\n", plan.AssuranceLevel)
	fmt.Fprintf(&output, "Connection: %s (%s)\n\n", plan.Integration.Mode, plan.Integration.Artifact)
	output.WriteString("Required OSS roles:\n")
	for _, role := range plan.Roles {
		fmt.Fprintf(&output, "- %s [%s]: %s\n", role.Artifact, role.Plane, role.Purpose)
	}
	output.WriteString("\nOpaque references (not resolved):\n")
	for _, reference := range plan.References {
		fmt.Fprintf(&output, "- %s: %s\n", reference.Field, reference.Reference)
	}
	output.WriteString("\nOperator prerequisites (not verified):\n")
	for _, prerequisite := range plan.Prerequisites {
		fmt.Fprintf(&output, "- [%s] %s\n", prerequisite.ID, prerequisite.Summary)
	}
	output.WriteString("\nNo changes were applied.\n")
	output.WriteString("No network, cluster, or platform was contacted.\n")
	return output.String()
}

// RenderResourceBoundPlanHuman gives the default operator view the same
// desired-state identity and unverified-readiness semantics as plan JSON v2.
func RenderResourceBoundPlanHuman(resource Resource, desiredStateDigest string, plan Plan) string {
	var output strings.Builder
	fmt.Fprintf(&output, "Desired state: %s %s %s\n", resource.APIVersion, resource.Kind, resource.Metadata.Name)
	fmt.Fprintf(&output, "Digest: %s\n", desiredStateDigest)
	output.WriteString("Readiness: unverified\n\n")
	output.WriteString(RenderPlanHuman(resource, plan))
	return output.String()
}
