// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package installtarget

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
)

const validDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func publicKey() string {
	return "ed25519:" + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
}

func validMap(profile string) map[string]any {
	spec := map[string]any{
		"deploymentRef": map[string]any{"name": "regulated-agent", "digest": validDigest},
		"distribution":  map[string]any{"artifactRef": "oci://registry.example.com/singleaxis/fabric", "version": "1.2.3", "digest": validDigest},
		"profile":       map[string]any{"name": profile, "digest": validDigest},
		"backend": map[string]any{
			"type": "helm",
			"helm": map[string]any{"context": "production-cluster", "namespace": "singleaxis", "releaseName": "fabric", "createNamespace": true},
		},
	}
	if profile == ProfileHighRisk {
		spec["bindings"] = map[string]any{
			"tenantId": "tenant-regulated",
			"exporter": map[string]any{
				"endpoint": "https://otlp.example.com/v1/traces",
				"egress": map[string]any{
					"cidrs": []any{"203.0.113.8/32", "2001:db8::/64"},
					"ports": []any{map[string]any{"protocol": "TCP", "port": 443}},
				},
			},
			"updateTrust": map[string]any{"keyId": "release-root-2026", "publicKey": publicKey()},
		}
	}
	return map[string]any{
		"apiVersion": APIVersion,
		"kind":       Kind,
		"metadata":   map[string]any{"name": "production-target"},
		"spec":       spec,
	}
}

func TestValidateProfiles(t *testing.T) {
	for _, profile := range []string{ProfilePermissiveDev, ProfileHighRisk} {
		t.Run(profile, func(t *testing.T) {
			resource, diagnostics := Validate(validMap(profile))
			if resource == nil || len(diagnostics) != 0 {
				t.Fatalf("Validate() = %#v, %#v", resource, diagnostics)
			}
		})
	}

	dev := validMap(ProfilePermissiveDev)
	dev["spec"].(map[string]any)["bindings"] = validMap(ProfileHighRisk)["spec"].(map[string]any)["bindings"]
	assertDiagnostic(t, dev, "installtarget.profile.bindings_forbidden")

	highRisk := validMap(ProfileHighRisk)
	delete(highRisk["spec"].(map[string]any), "bindings")
	assertDiagnostic(t, highRisk, "installtarget.profile.bindings_required")
}

func TestContractFixtures(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate package")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "management", "v1alpha1", "install-target"))
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

func TestStrictShapeAndTypes(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string]any)
		id   string
	}{
		{"wrong api version", func(v map[string]any) { v["apiVersion"] = "v1" }, "installtarget.field.value"},
		{"unknown root", func(v map[string]any) { v["vendor"] = "private" }, "installtarget.field.unknown"},
		{"unknown nested", func(v map[string]any) { v["spec"].(map[string]any)["backend"].(map[string]any)["mode"] = "apply" }, "installtarget.field.unknown"},
		{"required absent", func(v map[string]any) { delete(v["spec"].(map[string]any), "distribution") }, "installtarget.field.required"},
		{"wrong bool type", func(v map[string]any) {
			v["spec"].(map[string]any)["backend"].(map[string]any)["helm"].(map[string]any)["createNamespace"] = "yes"
		}, "installtarget.field.type"},
		{"unsupported backend", func(v map[string]any) { v["spec"].(map[string]any)["backend"].(map[string]any)["type"] = "compose" }, "installtarget.field.value"},
		{"non-semver version", func(v map[string]any) {
			v["spec"].(map[string]any)["distribution"].(map[string]any)["version"] = "latest"
		}, "installtarget.field.value"},
		{"invalid release name", func(v map[string]any) {
			v["spec"].(map[string]any)["backend"].(map[string]any)["helm"].(map[string]any)["releaseName"] = "fabric.release"
		}, "installtarget.field.value"},
		{"uppercase digest", func(v map[string]any) {
			v["spec"].(map[string]any)["profile"].(map[string]any)["digest"] = "sha256:ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
		}, "installtarget.digest.invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validMap(ProfileHighRisk)
			test.edit(value)
			assertDiagnostic(t, value, test.id)
		})
	}
}

func TestDistributionAndEndpointSafety(t *testing.T) {
	for _, artifact := range []string{
		"https://registry.example.com/fabric",
		"oci://user:password@registry.example.com/fabric",
		"oci://registry.example.com/fabric?token=secret",
		"oci://registry.example.com/fabric#fragment",
		"oci://registry.example.com",
		"oci://registry.example.com/fabric\nnext",
	} {
		value := validMap(ProfileHighRisk)
		value["spec"].(map[string]any)["distribution"].(map[string]any)["artifactRef"] = artifact
		assertDiagnostic(t, value, "installtarget.distribution.artifact_ref")
	}
	for _, endpoint := range []string{
		"http://otlp.example.com",
		"https://user:password@otlp.example.com",
		"https://otlp.example.com?token=value",
		"https://otlp.example.com/#fragment",
		"https://otlp..example.com",
		"https://otlp.example.com:99999",
	} {
		value := validMap(ProfileHighRisk)
		value["spec"].(map[string]any)["bindings"].(map[string]any)["exporter"].(map[string]any)["endpoint"] = endpoint
		assertDiagnostic(t, value, "installtarget.bindings.endpoint")
	}
}

func TestHighRiskEgressAndTrustValidation(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string]any)
		id   string
	}{
		{"default route", func(b map[string]any) {
			b["exporter"].(map[string]any)["egress"].(map[string]any)["cidrs"] = []any{"0.0.0.0/0"}
		}, "installtarget.bindings.cidr"},
		{"noncanonical CIDR", func(b map[string]any) {
			b["exporter"].(map[string]any)["egress"].(map[string]any)["cidrs"] = []any{"203.0.113.9/24"}
		}, "installtarget.bindings.cidr"},
		{"duplicate CIDR", func(b map[string]any) {
			b["exporter"].(map[string]any)["egress"].(map[string]any)["cidrs"] = []any{"203.0.113.8/32", "203.0.113.8/32"}
		}, "installtarget.bindings.cidr"},
		{"empty CIDRs", func(b map[string]any) { b["exporter"].(map[string]any)["egress"].(map[string]any)["cidrs"] = []any{} }, "installtarget.bindings.egress_required"},
		{"UDP", func(b map[string]any) {
			b["exporter"].(map[string]any)["egress"].(map[string]any)["ports"] = []any{map[string]any{"protocol": "UDP", "port": 443}}
		}, "installtarget.bindings.port"},
		{"port zero", func(b map[string]any) {
			b["exporter"].(map[string]any)["egress"].(map[string]any)["ports"] = []any{map[string]any{"protocol": "TCP", "port": 0}}
		}, "installtarget.bindings.port"},
		{"port too high", func(b map[string]any) {
			b["exporter"].(map[string]any)["egress"].(map[string]any)["ports"] = []any{map[string]any{"protocol": "TCP", "port": 65536}}
		}, "installtarget.bindings.port"},
		{"short key", func(b map[string]any) { b["updateTrust"].(map[string]any)["publicKey"] = "ed25519:AA" }, "installtarget.bindings.public_key"},
		{"padded key", func(b map[string]any) {
			b["updateTrust"].(map[string]any)["publicKey"] = base64.StdEncoding.EncodeToString(make([]byte, 32))
		}, "installtarget.bindings.public_key"},
		{"wrong prefix", func(b map[string]any) {
			b["updateTrust"].(map[string]any)["publicKey"] = "rsa:" + strings.Repeat("A", 43)
		}, "installtarget.bindings.public_key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validMap(ProfileHighRisk)
			test.edit(value["spec"].(map[string]any)["bindings"].(map[string]any))
			assertDiagnostic(t, value, test.id)
		})
	}
}

func TestArrayCapsAndCredentialLookingReferences(t *testing.T) {
	value := validMap(ProfileHighRisk)
	binding := value["spec"].(map[string]any)["bindings"].(map[string]any)
	cidrs := make([]any, 65)
	for index := range cidrs {
		cidrs[index] = "10.0.0.1/32"
	}
	binding["exporter"].(map[string]any)["egress"].(map[string]any)["cidrs"] = cidrs
	assertDiagnostic(t, value, "installtarget.field.length")

	value = validMap(ProfileHighRisk)
	binding = value["spec"].(map[string]any)["bindings"].(map[string]any)
	ports := make([]any, 17)
	for index := range ports {
		ports[index] = map[string]any{"protocol": "TCP", "port": index + 1}
	}
	binding["exporter"].(map[string]any)["egress"].(map[string]any)["ports"] = ports
	assertDiagnostic(t, value, "installtarget.field.length")

	for _, mutate := range []func(map[string]any){
		func(v map[string]any) {
			v["metadata"].(map[string]any)["name"] = "0123456789abcdef0123456789abcdef0123456789abcdef"
		},
		func(v map[string]any) {
			v["spec"].(map[string]any)["deploymentRef"].(map[string]any)["name"] = "0123456789abcdef0123456789abcdef0123456789abcdef"
		},
		func(v map[string]any) {
			v["spec"].(map[string]any)["backend"].(map[string]any)["helm"].(map[string]any)["context"] = "ghp_abcdefghijklmnopqrstuvwxyz123456"
		},
		func(v map[string]any) {
			v["spec"].(map[string]any)["bindings"].(map[string]any)["tenantId"] = "AKIAIOSFODNN7EXAMPLE"
		},
		func(v map[string]any) {
			v["spec"].(map[string]any)["bindings"].(map[string]any)["updateTrust"].(map[string]any)["keyId"] = "ghp_abcdefghijklmnopqrstuvwxyz123456"
		},
	} {
		value := validMap(ProfileHighRisk)
		mutate(value)
		assertDiagnostic(t, value, "installtarget.reference.sensitive")
	}

	for _, mutate := range []func(map[string]any){
		func(v map[string]any) {
			v["spec"].(map[string]any)["distribution"].(map[string]any)["artifactRef"] = "oci://registry.example.com/ghp_abcdefghijklmnopqrstuvwxyz123456"
		},
		func(v map[string]any) {
			v["spec"].(map[string]any)["bindings"].(map[string]any)["exporter"].(map[string]any)["endpoint"] = "https://otlp.example.com/ghp_abcdefghijklmnopqrstuvwxyz123456"
		},
	} {
		value := validMap(ProfileHighRisk)
		mutate(value)
		resource, diagnostics := Validate(value)
		if resource != nil || len(diagnostics) == 0 {
			t.Fatalf("Validate() = %#v, %#v", resource, diagnostics)
		}
	}

	value = validMap(ProfileHighRisk)
	value["spec"].(map[string]any)["API-Key"] = "must-not-echo"
	assertDiagnostic(t, value, "installtarget.security.inline_sensitive_value")
}

func TestLoadBytesRejectsAmbiguousDangerousAndOversizedInput(t *testing.T) {
	tests := []struct {
		name, format, content, id string
	}{
		{"duplicate YAML", "yaml", "kind: one\nkind: two\n", "installtarget.document.syntax"},
		{"duplicate JSON", "json", `{"kind":"one","kind":"two"}`, "installtarget.document.syntax"},
		{"anchor", "yaml", "kind: &kind value\n", "installtarget.document.alias_forbidden"},
		{"alias", "yaml", "kind: &kind value\ncopy: *kind\n", "installtarget.document.alias_forbidden"},
		{"unsafe tag", "yaml", "value: !!python/object:unsafe {}\n", "installtarget.document.syntax"},
		{"multiple YAML", "yaml", "one: 1\n---\ntwo: 2\n", "installtarget.document.syntax"},
		{"multiple JSON", "json", "{} {}", "installtarget.document.syntax"},
		{"unknown format", "toml", "x = 1", "installtarget.document.format"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(test.content), test.format)
			assertDocumentErrorID(t, err, test.id)
		})
	}
	_, err := LoadBytes([]byte{0xff}, "yaml")
	assertDocumentErrorID(t, err, "installtarget.document.encoding")
	_, err = LoadBytes(make([]byte, MaxDocumentBytes+1), "yaml")
	assertDocumentErrorID(t, err, "installtarget.file.too_large")
}

func TestParseFileJSONAndYAML(t *testing.T) {
	jsonDocument := `{"apiVersion":"fabric.singleaxis.dev/v1alpha1","kind":"FabricInstallTarget","metadata":{"name":"dev-target"},"spec":{"deploymentRef":{"name":"agent","digest":"` + validDigest + `"},"distribution":{"artifactRef":"oci://registry.example.com/fabric","version":"1.0.0","digest":"` + validDigest + `"},"profile":{"name":"permissive-dev","digest":"` + validDigest + `"},"backend":{"type":"helm","helm":{"context":"kind-dev","namespace":"singleaxis","releaseName":"fabric","createNamespace":true}}}}`
	yamlDocument := strings.ReplaceAll(jsonDocument, `"`, `"`) // JSON is valid YAML and exercises extension routing.
	for extension, content := range map[string]string{".json": jsonDocument, ".yaml": yamlDocument} {
		path := filepath.Join(t.TempDir(), "target"+extension)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		parsed, diagnostics, err := ParseFile(path)
		if err != nil || len(diagnostics) != 0 || parsed == nil {
			t.Fatalf("ParseFile() = %#v, %#v, %v", parsed, diagnostics, err)
		}
	}
}

func TestFileLoaderRejectsMissingDirectoryAndSymlinkWithoutLeakingPath(t *testing.T) {
	temp := t.TempDir()
	missing := filepath.Join(temp, "customer-secret-target.yaml")
	_, err := LoadFile(missing)
	assertDocumentErrorID(t, err, "installtarget.file.not_found")
	if strings.Contains(err.Error(), filepath.Base(missing)) {
		t.Fatal("diagnostic leaked file name")
	}
	_, err = LoadFile(temp)
	assertDocumentErrorID(t, err, "installtarget.file.not_regular")
	regular := filepath.Join(temp, "regular.yaml")
	if err := os.WriteFile(regular, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(temp, "link.yaml")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err = LoadFile(symlink)
	assertDocumentErrorID(t, err, "installtarget.file.not_regular")
}

func TestDigestIsDeterministicAcrossMapOrder(t *testing.T) {
	left := map[string]any{"z": 1, "a": map[string]any{"two": 2, "one": 1}}
	right := map[string]any{"a": map[string]any{"one": 1, "two": 2}, "z": 1}
	leftDigest, err := Digest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := Digest(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest || !digestPattern.MatchString(leftDigest) {
		t.Fatalf("digests = %q, %q", leftDigest, rightDigest)
	}
}

func TestSensitiveAndUnknownDiagnosticsDoNotEchoInput(t *testing.T) {
	secretKey := "password\x1b[31mcustomer"
	value := validMap(ProfileHighRisk)
	value["spec"].(map[string]any)[secretKey] = "extremely-secret-value"
	_, diagnostics := Validate(value)
	if len(diagnostics) == 0 {
		t.Fatal("expected diagnostics")
	}
	for _, item := range diagnostics {
		text := item.ID + item.Path + item.Summary
		if strings.Contains(text, secretKey) || strings.Contains(text, "extremely-secret-value") || strings.ContainsRune(text, '\x1b') {
			t.Fatalf("unsafe diagnostic: %#v", item)
		}
	}

	value = validMap(ProfileHighRisk)
	value["spec"].(map[string]any)["password"] = "never-echo-this"
	_, diagnostics = Validate(value)
	if len(diagnostics) != 1 || diagnostics[0].ID != "installtarget.security.inline_sensitive_value" {
		t.Fatalf("security diagnostics = %#v", diagnostics)
	}
}

func TestValidateAgainstDeployment(t *testing.T) {
	for _, test := range []struct {
		profile, assurance string
		wantDiagnostics    bool
	}{
		{ProfilePermissiveDev, "A0", false},
		{ProfilePermissiveDev, "A1", true},
		{ProfileHighRisk, "A2", false},
		{ProfileHighRisk, "A3", false},
		{ProfileHighRisk, "A1", true},
	} {
		t.Run(test.profile+"/"+test.assurance, func(t *testing.T) {
			referenced := deployment.Resource{
				APIVersion: deployment.APIVersion,
				Kind:       deployment.Kind,
				Metadata:   deployment.Metadata{Name: "regulated-agent"},
				Spec:       deployment.Spec{AssuranceLevel: test.assurance},
			}
			digest, err := deployment.Digest(deploymentDocument(referenced))
			if err != nil {
				t.Fatal(err)
			}
			value := validMap(test.profile)
			value["spec"].(map[string]any)["deploymentRef"].(map[string]any)["digest"] = digest
			target, diagnostics := Validate(value)
			if len(diagnostics) != 0 {
				t.Fatalf("target validation: %#v", diagnostics)
			}
			diagnostics = ValidateAgainstDeployment(*target, referenced)
			if (len(diagnostics) != 0) != test.wantDiagnostics {
				t.Fatalf("compatibility diagnostics = %#v", diagnostics)
			}
			if test.wantDiagnostics && diagnostics[0].ID != "installtarget.compatibility.assurance" {
				t.Fatalf("compatibility diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestCrossResourceDigestMatchesParsedDeploymentDocument(t *testing.T) {
	document := map[string]any{
		"apiVersion": deployment.APIVersion,
		"kind":       deployment.Kind,
		"metadata":   map[string]any{"name": "regulated-agent"},
		"spec": map[string]any{
			"assuranceLevel": "A2",
			"connection":     map[string]any{"mode": "gateway", "tenantIdFrom": "regulated-tenant"},
			"controls":       map[string]any{"profileRef": "controls-a2"},
			"observe":        map[string]any{"contentMode": "hash-only", "relayRef": "relay-a2"},
			"assurance":      map[string]any{"planRef": "assurance-a2"},
			"rollout":        map[string]any{"approvalRef": "approval-a2"},
		},
	}
	referenced, deploymentDiagnostics := deployment.Validate(document)
	if len(deploymentDiagnostics) != 0 {
		t.Fatal(deploymentDiagnostics)
	}
	digest, err := deployment.Digest(document)
	if err != nil {
		t.Fatal(err)
	}
	value := validMap(ProfileHighRisk)
	value["spec"].(map[string]any)["deploymentRef"].(map[string]any)["digest"] = digest
	target, diagnostics := Validate(value)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	if diagnostics = ValidateAgainstDeployment(*target, *referenced); len(diagnostics) != 0 {
		t.Fatalf("compatibility diagnostics = %#v", diagnostics)
	}
}

func TestValidateAgainstDeploymentDetectsIdentityWithoutEcho(t *testing.T) {
	referenced := deployment.Resource{APIVersion: deployment.APIVersion, Kind: deployment.Kind, Metadata: deployment.Metadata{Name: "actual-name"}, Spec: deployment.Spec{AssuranceLevel: "A0"}}
	value := validMap(ProfilePermissiveDev)
	target, diagnostics := Validate(value)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	diagnostics = ValidateAgainstDeployment(*target, referenced)
	ids := make([]string, len(diagnostics))
	for index, item := range diagnostics {
		ids[index] = item.ID
		text := item.ID + item.Path + item.Summary
		if strings.Contains(text, referenced.Metadata.Name) || strings.Contains(text, validDigest) {
			t.Fatalf("compatibility diagnostic leaked identity: %#v", item)
		}
	}
	if !reflect.DeepEqual(ids, []string{"installtarget.compatibility.name", "installtarget.compatibility.digest"}) {
		t.Fatalf("diagnostic IDs = %#v", ids)
	}
}

func assertDiagnostic(t *testing.T, value any, id string) {
	t.Helper()
	resource, diagnostics := Validate(value)
	if resource != nil {
		t.Fatalf("Validate() returned resource for invalid document: %#v", resource)
	}
	for _, item := range diagnostics {
		if item.ID == id {
			return
		}
	}
	t.Fatalf("missing diagnostic %q in %#v", id, diagnostics)
}

func assertDocumentErrorID(t *testing.T, err error, id string) {
	t.Helper()
	var documentErr *DocumentError
	if !errors.As(err, &documentErr) || documentErr.Diagnostic.ID != id {
		t.Fatalf("error = %#v, want DocumentError %q", err, id)
	}
}
