// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

func TestResourceBoundPlanEnvelopeCoversExactDesiredState(t *testing.T) {
	path := filepath.Join(contractRoot(t), "valid", "a3-regulated.json")
	parsed, diagnostics, err := ParseFile(path)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("ParseFile() = %#v, %v", diagnostics, err)
	}
	digest, err := Digest(parsed.Document)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "sha256:b4f3dd1da04cb2fd16c3df5678e97501d4b31819c219269de904e545f4bc6f78"
	if digest != expected {
		t.Fatalf("desired-state digest = %q, want %q", digest, expected)
	}

	envelopeValue, err := NewResourceBoundPlanEnvelope(parsed.Resource, digest, BuildPlan(parsed.Resource))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := RenderJSON(envelopeValue)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		SchemaVersion string `json:"schema_version"`
		Readiness     string `json:"readiness"`
		Resource      struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Name       string `json:"name"`
			Digest     string `json:"digest"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != "fabricctl.deployment-plan/v2" || envelope.Readiness != "unverified" {
		t.Fatalf("plan identity/readiness = %#v", envelope)
	}
	if envelope.Resource.APIVersion != APIVersion || envelope.Resource.Kind != Kind ||
		envelope.Resource.Name != "payments-agent-prod" || envelope.Resource.Digest != expected {
		t.Fatalf("resource binding = %#v", envelope.Resource)
	}
	if strings.Contains(string(payload), `"readiness": "verified"`) {
		t.Fatal("offline plan claimed verified readiness")
	}
	for _, invalid := range []string{"", "sha256:missing", "SHA256:b4f3dd1da04cb2fd16c3df5678e97501d4b31819c219269de904e545f4bc6f78"} {
		if _, err := NewResourceBoundPlanEnvelope(parsed.Resource, invalid, BuildPlan(parsed.Resource)); err == nil {
			t.Fatalf("accepted invalid desired-state digest %q", invalid)
		}
	}

	// The compatibility constructor intentionally remains resource-unbound.
	legacy, err := RenderJSON(NewPlanEnvelope(BuildPlan(parsed.Resource)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), `"resource"`) || strings.Contains(string(legacy), `"readiness"`) {
		t.Fatalf("legacy plan envelope changed unexpectedly: %s", legacy)
	}
}

func TestHumanDiagnosticPathsCannotInjectTerminalControls(t *testing.T) {
	path := "$.[evil\x1b[2J\n\x00\u202E]"
	diagnostics := []Diagnostic{{
		ID: "deployment.field.unknown", Severity: "error", Path: path,
		Summary: "Unknown deployment field is forbidden",
	}}
	human := RenderValidationHuman(diagnostics)
	for _, r := range human {
		if !unicode.IsPrint(r) && r != '\n' {
			t.Fatalf("human diagnostic contains non-printing rune %U: %q", r, human)
		}
	}
	for _, escaped := range []string{`\u001B`, `\u000A`, `\u0000`, `\u202E`} {
		if !strings.Contains(human, escaped) {
			t.Fatalf("human diagnostic does not visibly escape %q: %q", escaped, human)
		}
	}
	if strings.Contains(human, "\x1b[2J") {
		t.Fatalf("human diagnostic retained an active terminal escape: %q", human)
	}

	first, err := RenderJSON(NewValidationEnvelope(diagnostics))
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderJSON(NewValidationEnvelope(diagnostics))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("JSON diagnostic output is not deterministic")
	}
	var decoded ValidationEnvelope
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Diagnostics) != 1 || decoded.Diagnostics[0].Path != path {
		t.Fatalf("JSON diagnostic path was mutated: %#v", decoded.Diagnostics)
	}
}

func TestLoadFileRejectsDirectoriesAndSymlinks(t *testing.T) {
	directory := t.TempDir()
	assertDocumentError(t, directory, "deployment.file.not_regular")

	target := filepath.Join(directory, "target.yaml")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	assertDocumentError(t, link, "deployment.file.not_regular")
}
