// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/installtarget"
)

const (
	VerificationReportSchema = "fabricctl.bundle-verification-report/v1"
	maxArtifactBytes         = 1_048_576
)

var (
	digestHexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	dnsLabelPattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	secretKeyPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,251}[A-Za-z0-9])?$`)
	referencePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._:/-]{0,251}[A-Za-z0-9])?$`)
)

// Report is a value-free, machine-readable result of local bundle
// verification. Pass means internal consistency only, never readiness.
type Report struct {
	SchemaVersion string             `json:"schema_version"`
	Scope         string             `json:"scope"`
	Status        string             `json:"status"`
	Readiness     string             `json:"readiness"`
	Operation     VerificationEffect `json:"operation"`
	BundleDigest  string             `json:"bundle_digest,omitempty"`
	Checks        []Check            `json:"checks"`
	Diagnostics   []Diagnostic       `json:"diagnostics"`
}

type VerificationEffect struct {
	Network  bool `json:"network"`
	Mutating bool `json:"mutating"`
}

type Check struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type Diagnostic struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

var expectedPaths = []string{
	DeploymentFileName,
	InstallTargetFileName,
	ValuesFileName,
	SecretsRequiredFileName,
	InstallationPlanFileName,
	ManifestFileName,
}

// VerifyDirectory verifies exactly one Offline Install Bundle v1 without
// network access, process execution, reference resolution, or mutation.
func VerifyDirectory(dir string) Report {
	report := Report{
		SchemaVersion: VerificationReportSchema,
		Scope:         "offline",
		Status:        "fail",
		Readiness:     "unverified",
		Operation:     VerificationEffect{Network: false, Mutating: false},
		Checks:        make([]Check, 0, 7),
		Diagnostics:   make([]Diagnostic, 0, 1),
	}
	fail := func(checkID, diagnosticID, summary string) Report {
		report.Checks = append(report.Checks, Check{ID: checkID, Status: "fail"})
		report.Diagnostics = append(report.Diagnostics, Diagnostic{ID: diagnosticID, Severity: "error", Summary: summary})
		return report
	}

	info, err := os.Lstat(dir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fail("bundle.directory", "bundle.directory.invalid", "Bundle directory must identify one readable directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fail("bundle.directory", "bundle.directory.unreadable", "Bundle directory cannot be read")
	}
	if !hasExactPaths(entries) {
		return fail("bundle.contents", "bundle.contents.invalid", "Bundle directory must contain exactly the six allowlisted artifacts")
	}
	report.Checks = append(report.Checks, Check{ID: "bundle.contents", Status: "pass"})

	payloads := make(map[string][]byte, len(expectedPaths))
	for _, name := range expectedPaths {
		payload, err := readArtifact(filepath.Join(dir, name))
		if err != nil {
			return fail("bundle.artifacts", "bundle.artifact.invalid", "Every bundle artifact must be a bounded regular file")
		}
		payloads[name] = payload
	}
	report.Checks = append(report.Checks, Check{ID: "bundle.artifacts", Status: "pass"})

	decodedManifest, err := parseManifest(payloads[ManifestFileName])
	if err != nil {
		return fail("bundle.manifest", "bundle.manifest.invalid", "Bundle manifest does not satisfy the strict v1 contract")
	}
	canonicalManifest, err := renderJSON(decodedManifest)
	if err != nil || !bytes.Equal(canonicalManifest, payloads[ManifestFileName]) {
		return fail("bundle.manifest", "bundle.manifest.noncanonical", "Bundle manifest bytes are not the canonical v1 representation")
	}
	if err := verifyManifest(decodedManifest, payloads); err != nil {
		return fail("bundle.integrity", "bundle.integrity.mismatch", "Bundle artifact or manifest identity does not match exact bytes")
	}
	report.Checks = append(report.Checks,
		Check{ID: "bundle.manifest", Status: "pass"},
		Check{ID: "bundle.integrity", Status: "pass"},
	)

	deploymentDocument, err := deployment.LoadBytes(payloads[DeploymentFileName], "yaml")
	if err != nil {
		return fail("bundle.deployment", "bundle.deployment.invalid", "Canonical deployment does not satisfy the strict contract")
	}
	deploymentResource, deploymentDiagnostics := deployment.Validate(deploymentDocument)
	if len(deploymentDiagnostics) != 0 || deploymentResource == nil {
		return fail("bundle.deployment", "bundle.deployment.invalid", "Canonical deployment does not satisfy the strict contract")
	}
	canonicalDeployment, err := renderDeployment(*deploymentResource)
	if err != nil || !bytes.Equal(canonicalDeployment, payloads[DeploymentFileName]) {
		return fail("bundle.deployment", "bundle.deployment.noncanonical", "Deployment bytes are not the canonical bundle representation")
	}
	deploymentDigest, err := deployment.DigestResource(*deploymentResource)
	if err != nil {
		return fail("bundle.deployment", "bundle.deployment.invalid", "Canonical deployment identity cannot be computed")
	}
	report.Checks = append(report.Checks, Check{ID: "bundle.deployment", Status: "pass"})

	targetDocument, err := installtarget.LoadBytes(payloads[InstallTargetFileName], "yaml")
	if err != nil {
		return fail("bundle.target", "bundle.target.invalid", "Canonical install target does not satisfy the strict contract")
	}
	targetResource, targetDiagnostics := installtarget.Validate(targetDocument)
	if len(targetDiagnostics) != 0 || targetResource == nil {
		return fail("bundle.target", "bundle.target.invalid", "Canonical install target does not satisfy the strict contract")
	}
	canonicalTarget, err := renderInstallTarget(*targetResource)
	if err != nil || !bytes.Equal(canonicalTarget, payloads[InstallTargetFileName]) {
		return fail("bundle.target", "bundle.target.noncanonical", "Install-target bytes are not the canonical bundle representation")
	}
	targetDigest, err := installtarget.Digest(*targetResource)
	if err != nil || len(installtarget.ValidateAgainstDeployment(*targetResource, *deploymentResource)) != 0 {
		return fail("bundle.binding", "bundle.binding.invalid", "Canonical resources are not bound to the same reviewed deployment")
	}
	report.Checks = append(report.Checks,
		Check{ID: "bundle.target", Status: "pass"},
		Check{ID: "bundle.binding", Status: "pass"},
	)

	valuesPayload, err := renderValues(*deploymentResource, *targetResource)
	if err != nil || !bytes.Equal(valuesPayload, payloads[ValuesFileName]) {
		return fail("bundle.values", "bundle.values.stale", "Helm values are invalid or stale for the canonical resources")
	}
	report.Checks = append(report.Checks, Check{ID: "bundle.values", Status: "pass"})

	plan, err := parsePlan(payloads[InstallationPlanFileName])
	expectedPlan := buildPlan(*deploymentResource, deploymentDigest, *targetResource, targetDigest)
	canonicalPlan, renderPlanErr := renderJSON(expectedPlan)
	if err != nil || renderPlanErr != nil || !reflect.DeepEqual(plan, expectedPlan) || !bytes.Equal(canonicalPlan, payloads[InstallationPlanFileName]) {
		return fail("bundle.plan", "bundle.plan.stale", "Installation plan is invalid or stale for the canonical resources")
	}
	report.Checks = append(report.Checks, Check{ID: "bundle.plan", Status: "pass"})

	if err := verifySecretRequirements(payloads[SecretsRequiredFileName], *targetResource); err != nil {
		return fail("bundle.secret_requirements", "bundle.secret_requirements.invalid", "Secret requirements are invalid, value-bearing, or inconsistent with the selected profile")
	}
	expectedSecrets, err := renderSecretRequirements(*targetResource)
	if err != nil || !bytes.Equal(expectedSecrets, payloads[SecretsRequiredFileName]) {
		return fail("bundle.secret_requirements", "bundle.secret_requirements.noncanonical", "Secret requirements are not the canonical bundle representation")
	}
	report.Checks = append(report.Checks, Check{ID: "bundle.secret_requirements", Status: "pass"})
	report.Status = "pass"
	report.BundleDigest = decodedManifest.BundleDigest
	return report
}

func hasExactPaths(entries []os.DirEntry) bool {
	if len(entries) != len(expectedPaths) {
		return false
	}
	want := append([]string(nil), expectedPaths...)
	sort.Strings(want)
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	sort.Strings(got)
	return reflect.DeepEqual(got, want)
}

func readArtifact(path string) ([]byte, error) {
	file, err := openRegularArtifact(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > maxArtifactBytes {
		return nil, errArtifactNotRegular
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxArtifactBytes+1))
	if err != nil || len(payload) > maxArtifactBytes {
		return nil, errArtifactNotRegular
	}
	return payload, nil
}

func strictJSON(payload []byte, target any) error {
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

func parseManifest(payload []byte) (manifest, error) {
	var decoded manifest
	if err := strictJSON(payload, &decoded); err != nil {
		return manifest{}, err
	}
	if decoded.SchemaVersion != "fabricctl.bundle-manifest/v1" || decoded.SelfExclusion != ManifestFileName ||
		decoded.Generator.Name != "fabricctl" || !generatorVersionPattern.MatchString(decoded.Generator.Version) ||
		!generatorCommitPattern.MatchString(decoded.Generator.Commit) || len(decoded.Files) != 5 ||
		!strings.HasPrefix(decoded.BundleDigest, "sha256:") || !digestHexPattern.MatchString(strings.TrimPrefix(decoded.BundleDigest, "sha256:")) {
		return manifest{}, fmt.Errorf("invalid manifest contract")
	}
	expectedManifestPaths := []string{ValuesFileName, InstallTargetFileName, InstallationPlanFileName, SecretsRequiredFileName, DeploymentFileName}
	seen := make(map[string]bool, len(decoded.Files))
	for index, entry := range decoded.Files {
		if entry.Path == ManifestFileName || !isManifestPath(entry.Path) || seen[entry.Path] || !digestHexPattern.MatchString(entry.SHA256) {
			return manifest{}, fmt.Errorf("invalid manifest entry")
		}
		if entry.Path != expectedManifestPaths[index] {
			return manifest{}, fmt.Errorf("noncanonical manifest entry order")
		}
		seen[entry.Path] = true
	}
	if len(seen) != 5 {
		return manifest{}, fmt.Errorf("incomplete manifest")
	}
	return decoded, nil
}

func isManifestPath(path string) bool {
	switch path {
	case DeploymentFileName, InstallTargetFileName, ValuesFileName, SecretsRequiredFileName, InstallationPlanFileName:
		return true
	default:
		return false
	}
}

func verifyManifest(decoded manifest, payloads map[string][]byte) error {
	for _, entry := range decoded.Files {
		if digestHex(payloads[entry.Path]) != entry.SHA256 {
			return fmt.Errorf("artifact digest mismatch")
		}
	}
	digest, err := digestManifestEntries(decoded.Files)
	if err != nil || digest != decoded.BundleDigest {
		return fmt.Errorf("bundle digest mismatch")
	}
	return nil
}

func parsePlan(payload []byte) (installationPlan, error) {
	var plan installationPlan
	if err := strictJSON(payload, &plan); err != nil {
		return installationPlan{}, err
	}
	if plan.SchemaVersion != "fabricctl.installation-plan/v1" || plan.Status != "pass" || plan.Readiness != "unverified" ||
		plan.Operation.Network || plan.Operation.Mutating {
		return installationPlan{}, fmt.Errorf("invalid plan posture")
	}
	return plan, nil
}

func verifySecretRequirements(payload []byte, target installtarget.Resource) error {
	document, err := installtarget.LoadBytes(payload, "yaml")
	if err != nil {
		return err
	}
	if containsValueBearingKey(document) {
		return fmt.Errorf("value-bearing key")
	}
	object, ok := document.(map[string]any)
	if !ok || !hasOnlyKeys(object, "apiVersion", "kind", "metadata", "status", "requirements") ||
		stringValue(object["apiVersion"]) != installtarget.APIVersion || stringValue(object["kind"]) != "FabricSecretRequirements" ||
		stringValue(object["status"]) != "unresolved" {
		return fmt.Errorf("invalid secret requirement document")
	}
	metadata, ok := object["metadata"].(map[string]any)
	if !ok || !hasOnlyKeys(metadata, "name") || stringValue(metadata["name"]) != target.Metadata.Name {
		return fmt.Errorf("invalid secret requirement identity")
	}
	rawRequirements, ok := object["requirements"].([]any)
	if !ok || len(rawRequirements) > 128 {
		return fmt.Errorf("invalid secret requirements")
	}
	want := expectedSecretRequirements(target)
	if len(rawRequirements) != len(want) {
		return fmt.Errorf("incorrect secret requirements")
	}
	for index, raw := range rawRequirements {
		object, ok := raw.(map[string]any)
		if !ok || !hasOnlyKeys(object, "name", "namespace", "keys", "purpose", "consumer") {
			return fmt.Errorf("invalid secret requirement")
		}
		keys, ok := stringSlice(object["keys"])
		if !ok || len(keys) == 0 || len(keys) > 32 || hasDuplicateStrings(keys) {
			return fmt.Errorf("invalid secret keys")
		}
		actual := secretRequirement{Name: stringValue(object["name"]), Namespace: stringValue(object["namespace"]), Keys: keys,
			Purpose: stringValue(object["purpose"]), Consumer: stringValue(object["consumer"])}
		if !validSecretRequirement(actual) || !reflect.DeepEqual(actual, want[index]) {
			return fmt.Errorf("secret requirement is inconsistent")
		}
	}
	return nil
}

func expectedSecretRequirements(target installtarget.Resource) []secretRequirement {
	if target.Spec.Profile.Name != installtarget.ProfileHighRisk {
		return []secretRequirement{}
	}
	namespace := target.Spec.Backend.Helm.Namespace
	return []secretRequirement{
		{Name: "fabric-otel-receiver-tls", Namespace: namespace, Keys: []string{"tls.crt", "tls.key"}, Purpose: "otlp-receiver-server-identity", Consumer: "otel-collector"},
		{Name: "fabric-otel-client-ca", Namespace: namespace, Keys: []string{"ca.crt"}, Purpose: "otlp-client-certificate-verification", Consumer: "otel-collector"},
		{Name: "fabric-otel-export-auth", Namespace: namespace, Keys: []string{"authorization"}, Purpose: "authenticated-telemetry-export", Consumer: "otel-collector"},
		{Name: "fabric-otel-sampler-key", Namespace: namespace, Keys: []string{"hmac_key"}, Purpose: "deterministic-telemetry-sampling", Consumer: "otel-collector"},
		{Name: "fabric-presidio-tenant-key", Namespace: namespace, Keys: []string{"tenant.key"}, Purpose: "tenant-scoped-telemetry-pseudonymization", Consumer: "otel-collector/presidio"},
	}
}

func containsValueBearingKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(key))
			switch normalized {
			case "data", "stringdata", "value", "values", "secretvalue", "plaintext", "password", "token", "credential", "credentials":
				return true
			}
			if containsValueBearingKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsValueBearingKey(child) {
				return true
			}
		}
	}
	return false
}

func hasOnlyKeys(object map[string]any, allowed ...string) bool {
	if len(object) != len(allowed) {
		return false
	}
	for _, key := range allowed {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func stringSlice(value any) ([]string, bool) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, len(raw))
	for index, value := range raw {
		result[index], ok = value.(string)
		if !ok {
			return nil, false
		}
	}
	return result, true
}

func hasDuplicateStrings(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func validSecretRequirement(requirement secretRequirement) bool {
	if !dnsLabelPattern.MatchString(requirement.Name) || !dnsLabelPattern.MatchString(requirement.Namespace) ||
		!referencePattern.MatchString(requirement.Purpose) || !referencePattern.MatchString(requirement.Consumer) {
		return false
	}
	for _, key := range requirement.Keys {
		if !secretKeyPattern.MatchString(key) {
			return false
		}
	}
	return true
}
