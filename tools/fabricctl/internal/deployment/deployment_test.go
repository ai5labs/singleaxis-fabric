// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func contractRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "management", "v1alpha1"))
}

func validDocument(level string) map[string]any {
	modes := map[string]string{"A0": "sdk", "A1": "adapter", "A2": "gateway", "A3": "otlp"}
	spec := map[string]any{
		"assuranceLevel": level,
		"connection":     map[string]any{"mode": modes[level], "tenantIdFrom": "tenant-" + strings.ToLower(level)},
		"observe":        map[string]any{"contentMode": "hash-only"},
	}
	if level != "A0" {
		spec["observe"].(map[string]any)["relayRef"] = "relay-" + strings.ToLower(level)
	}
	if level == "A2" || level == "A3" {
		spec["controls"] = map[string]any{"profileRef": "controls-" + strings.ToLower(level)}
		spec["assurance"] = map[string]any{"planRef": "assurance-" + strings.ToLower(level)}
		spec["rollout"] = map[string]any{"approvalRef": "approval-" + strings.ToLower(level)}
	}
	if level == "A3" {
		spec["connection"].(map[string]any)["workloadIdentityRef"] = "regulated-workload-identity"
	}
	return map[string]any{
		"apiVersion": APIVersion, "kind": Kind,
		"metadata": map[string]any{"name": "agent-" + strings.ToLower(level)}, "spec": spec,
	}
}

func TestContractFixtures(t *testing.T) {
	root := contractRoot(t)
	valid, err := filepath.Glob(filepath.Join(root, "valid", "*"))
	if err != nil || len(valid) == 0 {
		t.Fatalf("valid fixture discovery: %v (%d files)", err, len(valid))
	}
	for _, path := range valid {
		t.Run("valid/"+filepath.Base(path), func(t *testing.T) {
			parsed, diagnostics, err := ParseFile(path)
			if err != nil || len(diagnostics) != 0 || parsed == nil {
				t.Fatalf("ParseFile() = %#v, %#v, %v", parsed, diagnostics, err)
			}
		})
	}
	invalid, err := filepath.Glob(filepath.Join(root, "invalid", "*"))
	if err != nil || len(invalid) == 0 {
		t.Fatalf("invalid fixture discovery: %v (%d files)", err, len(invalid))
	}
	for _, path := range invalid {
		t.Run("invalid/"+filepath.Base(path), func(t *testing.T) {
			parsed, diagnostics, err := ParseFile(path)
			if err != nil || parsed != nil || len(diagnostics) == 0 {
				t.Fatalf("ParseFile() = %#v, %#v, %v", parsed, diagnostics, err)
			}
		})
	}
}

func TestSafeLoadingRejectsAmbiguousOrDangerousDocuments(t *testing.T) {
	tests := []struct {
		name, format, content, diagnosticID string
	}{
		{"duplicate YAML", "yaml", "apiVersion: one\napiVersion: two\n", "deployment.document.syntax"},
		{"duplicate nested JSON", "json", `{"x":{"name":"one","name":"two"}}`, "deployment.document.syntax"},
		{"anchor only", "yaml", "name: &name value\n", "deployment.document.alias_forbidden"},
		{"alias", "yaml", "name: &name value\ncopy: *name\n", "deployment.document.alias_forbidden"},
		{"merge", "yaml", "base: &base {x: y}\nresult: {<<: *base}\n", "deployment.document.alias_forbidden"},
		{"unsafe tag", "yaml", "value: !!python/object:unsafe {}\n", "deployment.document.syntax"},
		{"multiple YAML docs", "yaml", "one: 1\n---\ntwo: 2\n", "deployment.document.syntax"},
		{"multiple JSON values", "json", "{} {}", "deployment.document.syntax"},
		{"empty", "yaml", "", "deployment.document.syntax"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(test.content), test.format)
			var documentErr *DocumentError
			if !errors.As(err, &documentErr) || documentErr.Diagnostic.ID != test.diagnosticID {
				t.Fatalf("LoadBytes() error = %#v", err)
			}
			if strings.Contains(err.Error(), "value") && test.name == "alias" {
				t.Fatal("diagnostic disclosed source content")
			}
		})
	}
}

func TestBoundedUTF8FileLoadingAndStableFileErrors(t *testing.T) {
	temp := t.TempDir()
	invalidUTF8 := filepath.Join(temp, "invalid.yaml")
	if err := os.WriteFile(invalidUTF8, []byte{0xff, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}
	assertDocumentError(t, invalidUTF8, "deployment.document.encoding")

	oversized := filepath.Join(temp, "oversized.yaml")
	if err := os.WriteFile(oversized, make([]byte, MaxDocumentBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	assertDocumentError(t, oversized, "deployment.file.too_large")
	assertDocumentError(t, filepath.Join(temp, "customer-secret-name.yaml"), "deployment.file.not_found")
}

func assertDocumentError(t *testing.T, path, id string) {
	t.Helper()
	_, err := LoadFile(path)
	var documentErr *DocumentError
	if !errors.As(err, &documentErr) || documentErr.Diagnostic.ID != id {
		t.Fatalf("LoadFile(%q) error = %#v", path, err)
	}
	if strings.Contains(documentErr.Diagnostic.Summary, filepath.Base(path)) {
		t.Fatal("document diagnostic leaked file name")
	}
}

func TestSensitiveKeyPrescanIsRecursiveValueFreeAndPreemptsShape(t *testing.T) {
	secret := "super-private-value"
	value := validDocument("A0")
	value["spec"].(map[string]any)["unknown"] = []any{map[string]any{"ApiKey": secret}}
	resource, diagnostics := Validate(value)
	if resource != nil || len(diagnostics) != 1 {
		t.Fatalf("Validate() = %#v, %#v", resource, diagnostics)
	}
	if diagnostics[0].ID != "deployment.security.inline_sensitive_value" || diagnostics[0].Path != "$.spec.unknown[0].ApiKey" {
		t.Fatalf("unexpected diagnostic: %#v", diagnostics[0])
	}
	payload, err := RenderJSON(NewValidationEnvelope(diagnostics))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), secret) {
		t.Fatal("rendered diagnostic disclosed secret")
	}
}

func TestStrictValidationAndReferenceDiagnostics(t *testing.T) {
	if resource, diagnostics := Validate([]any{"not", "a", "mapping"}); resource != nil || diagnostics[0].ID != "deployment.document.type" {
		t.Fatalf("wrong document diagnostic: %#v", diagnostics)
	}
	value := validDocument("A0")
	value["metadata"].(map[string]any)["name"] = "INVALID NAME"
	value["spec"].(map[string]any)["connection"].(map[string]any)["tenantIdFrom"] = "bad reference?"
	resource, diagnostics := Validate(value)
	if resource != nil || len(diagnostics) != 2 {
		t.Fatalf("Validate() = %#v, %#v", resource, diagnostics)
	}
	ids := []string{diagnostics[0].ID, diagnostics[1].ID}
	if !reflect.DeepEqual(ids, []string{"deployment.identity.invalid_name", "deployment.reference.invalid"}) {
		t.Fatalf("diagnostic IDs = %#v", ids)
	}

	value = validDocument("A0")
	value["spec"].(map[string]any)["vendorBackend"] = "singleaxis"
	_, diagnostics = Validate(value)
	if len(diagnostics) != 1 || diagnostics[0].ID != "deployment.field.unknown" {
		t.Fatalf("unknown field diagnostics = %#v", diagnostics)
	}
	value = validDocument("A0")
	value["metadata"].(map[string]any)["name"] = true
	_, diagnostics = Validate(value)
	if len(diagnostics) != 1 || diagnostics[0].ID != "deployment.field.type" {
		t.Fatalf("wrong type diagnostics = %#v", diagnostics)
	}

	for name, mutate := range map[string]func(map[string]any){
		"required reference empty": func(value map[string]any) {
			value["spec"].(map[string]any)["connection"].(map[string]any)["tenantIdFrom"] = ""
		},
		"optional reference present empty": func(value map[string]any) {
			value["spec"].(map[string]any)["connection"].(map[string]any)["workloadIdentityRef"] = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := validDocument("A0")
			mutate(value)
			_, diagnostics := Validate(value)
			if len(diagnostics) != 1 || diagnostics[0].ID != "deployment.reference.invalid" {
				t.Fatalf("empty reference diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestDeclarativeReferencesRejectCredentialLikeValuesWithoutEcho(t *testing.T) {
	for _, value := range []string{
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_abcdefghijklmnopqrstuvwxyz123456",
		"0123456789abcdef0123456789abcdef01234567",
	} {
		document := validDocument("A0")
		document["spec"].(map[string]any)["connection"].(map[string]any)["tenantIdFrom"] = value
		resource, diagnostics := Validate(document)
		if resource != nil || len(diagnostics) != 1 || diagnostics[0].ID != "deployment.reference.sensitive" {
			t.Fatalf("Validate(%q) = %#v, %#v", value, resource, diagnostics)
		}
		payload, err := RenderJSON(NewValidationEnvelope(diagnostics))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), value) {
			t.Fatalf("diagnostic echoed credential-like reference %q", value)
		}
	}
}

func TestAssuranceRequirements(t *testing.T) {
	for _, level := range []string{"A0", "A1", "A2", "A3"} {
		t.Run(level+" valid", func(t *testing.T) {
			resource, diagnostics := Validate(validDocument(level))
			if resource == nil || len(diagnostics) != 0 {
				t.Fatalf("Validate() = %#v, %#v", resource, diagnostics)
			}
		})
	}
	for _, level := range []string{"A1", "A2", "A3"} {
		t.Run(level+" missing relay", func(t *testing.T) {
			value := validDocument(level)
			delete(value["spec"].(map[string]any)["observe"].(map[string]any), "relayRef")
			_, diagnostics := Validate(value)
			if !hasDiagnostic(diagnostics, "deployment.assurance.requirements") {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
		})
	}
	value := validDocument("A3")
	delete(value["spec"].(map[string]any)["connection"].(map[string]any), "workloadIdentityRef")
	_, diagnostics := Validate(value)
	if len(diagnostics) != 1 || diagnostics[0].ID != "deployment.assurance.requirements" {
		t.Fatalf("A3 diagnostics = %#v", diagnostics)
	}
}

func hasDiagnostic(diagnostics []Diagnostic, id string) bool {
	for _, item := range diagnostics {
		if item.ID == id {
			return true
		}
	}
	return false
}

func TestCanonicalDigestMatchesPythonAndKeyOrder(t *testing.T) {
	tests := map[string]string{
		"a0-local.yaml":     "sha256:11ec85b379c27fcc3330758a001abfe1dae834da2f0dbda3f63352ebf261c96a",
		"a3-regulated.json": "sha256:b4f3dd1da04cb2fd16c3df5678e97501d4b31819c219269de904e545f4bc6f78",
	}
	for fixture, pythonDigest := range tests {
		t.Run(fixture, func(t *testing.T) {
			value, err := LoadFile(filepath.Join(contractRoot(t), "valid", fixture))
			if err != nil {
				t.Fatal(err)
			}
			digest, err := Digest(value)
			if err != nil {
				t.Fatal(err)
			}
			if digest != pythonDigest {
				t.Fatalf("Digest() = %q, want Python %q", digest, pythonDigest)
			}
		})
	}

	path := filepath.Join(contractRoot(t), "valid", "a3-regulated.json")
	value, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := Digest(value)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadBytes(raw, "json")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Digest(reloaded)
	if again != digest {
		t.Fatalf("digest changed after key reordering: %q != %q", again, digest)
	}
	value.(map[string]any)["metadata"].(map[string]any)["name"] = "payments-agent-canary"
	changed, _ := Digest(value)
	if changed == digest {
		t.Fatal("digest did not cover a declared field")
	}
}

func TestDigestResourceMatchesDecodedDocumentForOptionalA3Fields(t *testing.T) {
	parsed, diagnostics, err := ParseFile(filepath.Join(contractRoot(t), "valid", "a3-regulated.json"))
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("ParseFile() parsed=%#v diagnostics=%#v err=%v", parsed, diagnostics, err)
	}
	want, err := Digest(parsed.Document)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DigestResource(parsed.Resource)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("DigestResource() = %q, want decoded document %q", got, want)
	}
}

func TestDeterministicPlansForEveryLevel(t *testing.T) {
	tests := []struct {
		level, artifact   string
		roles, references []string
	}{
		{"A0", "Fabric SDK", []string{"deployment.role.connect", "deployment.role.collector"}, []string{"deployment.reference.tenant_identity"}},
		{"A1", "Fabric Adapter", []string{"deployment.role.connect", "deployment.role.collector", "deployment.role.relay"}, []string{"deployment.reference.tenant_identity", "deployment.reference.relay"}},
		{"A2", "Fabric Gateway", []string{"deployment.role.connect", "deployment.role.control", "deployment.role.collector", "deployment.role.relay", "deployment.role.assurance_runner"}, []string{"deployment.reference.tenant_identity", "deployment.reference.control_profile", "deployment.reference.relay", "deployment.reference.assurance_plan", "deployment.reference.rollout_approval"}},
		{"A3", "Fabric Collector OTLP receiver", []string{"deployment.role.connect", "deployment.role.control", "deployment.role.collector", "deployment.role.relay", "deployment.role.assurance_runner"}, []string{"deployment.reference.tenant_identity", "deployment.reference.workload_identity", "deployment.reference.control_profile", "deployment.reference.relay", "deployment.reference.assurance_plan", "deployment.reference.rollout_approval"}},
	}
	for _, test := range tests {
		t.Run(test.level, func(t *testing.T) {
			resource, diagnostics := Validate(validDocument(test.level))
			if len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			first := BuildPlan(*resource)
			second := BuildPlan(*resource)
			if !reflect.DeepEqual(first, second) || first.Integration.Artifact != test.artifact {
				t.Fatalf("non-deterministic or wrong plan: %#v / %#v", first, second)
			}
			if got := roleIDs(first.Roles); !reflect.DeepEqual(got, test.roles) {
				t.Fatalf("roles = %#v, want %#v", got, test.roles)
			}
			if got := referenceIDs(first.References); !reflect.DeepEqual(got, test.references) {
				t.Fatalf("references = %#v, want %#v", got, test.references)
			}
			for _, prerequisite := range first.Prerequisites {
				if prerequisite.Status != "required" {
					t.Fatalf("prerequisite incorrectly attested: %#v", prerequisite)
				}
			}
		})
	}
}

func roleIDs(roles []PlanRole) []string {
	result := make([]string, len(roles))
	for index, role := range roles {
		result[index] = role.ID
	}
	return result
}

func referenceIDs(references []PlanReference) []string {
	result := make([]string, len(references))
	for index, reference := range references {
		result[index] = reference.ID
	}
	return result
}

func TestA3PlanIncludesAllOpaqueReferencesInContractOrder(t *testing.T) {
	parsed, diagnostics, err := ParseFile(filepath.Join(contractRoot(t), "valid", "a3-regulated.json"))
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("ParseFile() errors = %#v, %v", diagnostics, err)
	}
	plan := BuildPlan(parsed.Resource)
	wantFields := []string{
		"spec.connection.tenantIdFrom", "spec.connection.workloadIdentityRef",
		"spec.controls.profileRef", "spec.controls.policyRef", "spec.controls.authorizationRef",
		"spec.controls.piiRef", "spec.controls.guardrailRef", "spec.controls.escalationRef",
		"spec.observe.relayRef", "spec.assurance.planRef", "spec.rollout.approvalRef",
	}
	fields := make([]string, len(plan.References))
	for index, reference := range plan.References {
		fields[index] = reference.Field
	}
	if !reflect.DeepEqual(fields, wantFields) {
		t.Fatalf("reference fields = %#v, want %#v", fields, wantFields)
	}
}

func TestRenderersAreStableAndExplicitlyOffline(t *testing.T) {
	resource, _ := Validate(validDocument("A0"))
	plan := BuildPlan(*resource)
	first, err := RenderJSON(NewPlanEnvelope(plan))
	if err != nil {
		t.Fatal(err)
	}
	second, _ := RenderJSON(NewPlanEnvelope(plan))
	if string(first) != string(second) || !strings.HasSuffix(string(first), "\n") {
		t.Fatal("JSON rendering is not stable")
	}
	var envelope map[string]any
	if err := json.Unmarshal(first, &envelope); err != nil {
		t.Fatal(err)
	}
	operation := envelope["operation"].(map[string]any)
	if operation["mode"] != "offline" || operation["mutating"] != false {
		t.Fatalf("operation = %#v", operation)
	}
	human := RenderPlanHuman(*resource, plan)
	for _, phrase := range []string{"Required OSS roles:", "Operator prerequisites (not verified):", "No changes were applied", "No network, cluster, or platform was contacted"} {
		if !strings.Contains(human, phrase) {
			t.Fatalf("human plan missing %q", phrase)
		}
	}
	validation, _ := RenderJSON(NewValidationEnvelope(nil))
	if !strings.Contains(string(validation), `"diagnostics": []`) {
		t.Fatalf("pass validation must use empty list: %s", validation)
	}
	boundHuman := RenderResourceBoundPlanHuman(
		*resource,
		"sha256:11ec85b379c27fcc3330758a001abfe1dae834da2f0dbda3f63352ebf261c96a",
		plan,
	)
	for _, phrase := range []string{
		"Desired state: fabric.singleaxis.dev/v1alpha1 FabricDeployment agent-a0",
		"Digest: sha256:11ec85b379c27fcc3330758a001abfe1dae834da2f0dbda3f63352ebf261c96a",
		"Readiness: unverified",
	} {
		if !strings.Contains(boundHuman, phrase) {
			t.Errorf("resource-bound human plan omits %q:\n%s", phrase, boundHuman)
		}
	}
}
