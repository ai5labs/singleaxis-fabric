// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeTestBundle(t *testing.T, level string) string {
	t.Helper()
	resource := testDeployment(level)
	built, err := Build(resource, testTarget(t, resource), Generator{
		Name: "fabricctl", Version: "0.7.1", Commit: strings.Repeat("e", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, artifact := range built.Artifacts {
		if err := os.WriteFile(filepath.Join(dir, artifact.Path), artifact.Payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func rewriteManifest(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, ManifestFileName)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded manifest
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	for index := range decoded.Files {
		artifact, err := os.ReadFile(filepath.Join(dir, decoded.Files[index].Path))
		if err != nil {
			t.Fatal(err)
		}
		decoded.Files[index].SHA256 = digestHex(artifact)
	}
	decoded.BundleDigest, err = digestManifestEntries(decoded.Files)
	if err != nil {
		t.Fatal(err)
	}
	payload, err = renderJSON(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func requireFailedCheck(t *testing.T, report Report, diagnosticID string) {
	t.Helper()
	if report.Status != "fail" || report.Readiness != "unverified" || report.Scope != "offline" ||
		report.Operation.Network || report.Operation.Mutating || len(report.Diagnostics) != 1 || report.Diagnostics[0].ID != diagnosticID {
		t.Fatalf("unexpected report: %#v", report)
	}
}

func TestVerifyDirectoryPassesExactGeneratedBundles(t *testing.T) {
	for _, level := range []string{"A0", "A3"} {
		t.Run(level, func(t *testing.T) {
			report := VerifyDirectory(writeTestBundle(t, level))
			if report.SchemaVersion != VerificationReportSchema || report.Status != "pass" || report.Scope != "offline" ||
				report.Readiness != "unverified" || report.Operation.Network || report.Operation.Mutating ||
				!strings.HasPrefix(report.BundleDigest, "sha256:") || len(report.Diagnostics) != 0 {
				t.Fatalf("VerifyDirectory() = %#v", report)
			}
			for _, check := range report.Checks {
				if check.Status != "pass" {
					t.Fatalf("failed check in pass report: %#v", report)
				}
			}
		})
	}
}

func TestVerifyDirectoryRejectsCorruption(t *testing.T) {
	dir := writeTestBundle(t, "A0")
	if err := os.WriteFile(filepath.Join(dir, ValuesFileName), []byte("changed: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requireFailedCheck(t, VerifyDirectory(dir), "bundle.integrity.mismatch")
}

func TestVerifyDirectoryRejectsMissingAndExtraArtifacts(t *testing.T) {
	for _, mutate := range []func(*testing.T, string){
		func(t *testing.T, dir string) { t.Helper(); mustRemove(t, filepath.Join(dir, ValuesFileName)) },
		func(t *testing.T, dir string) {
			t.Helper()
			mustWrite(t, filepath.Join(dir, "unexpected.txt"), []byte("x"))
		},
	} {
		dir := writeTestBundle(t, "A0")
		mutate(t, dir)
		requireFailedCheck(t, VerifyDirectory(dir), "bundle.contents.invalid")
	}
}

func TestVerifyDirectoryRejectsSymlinkArtifact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires platform privileges")
	}
	dir := writeTestBundle(t, "A0")
	target := filepath.Join(dir, ValuesFileName)
	payload, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	mustRemove(t, target)
	realPath := filepath.Join(t.TempDir(), "values.yaml")
	mustWrite(t, realPath, payload)
	if err := os.Symlink(realPath, target); err != nil {
		t.Fatal(err)
	}
	requireFailedCheck(t, VerifyDirectory(dir), "bundle.artifact.invalid")
}

func TestVerifyDirectoryRejectsOversizeArtifact(t *testing.T) {
	dir := writeTestBundle(t, "A0")
	file, err := os.OpenFile(filepath.Join(dir, ValuesFileName), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxArtifactBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	requireFailedCheck(t, VerifyDirectory(dir), "bundle.artifact.invalid")
}

func TestVerifyDirectoryRejectsManifestMismatchAndDuplicates(t *testing.T) {
	for _, mutate := range []func(*manifest){
		func(value *manifest) { value.BundleDigest = "sha256:" + strings.Repeat("0", 64) },
		func(value *manifest) { value.Files[1] = value.Files[0] },
		func(value *manifest) { value.SelfExclusion = ValuesFileName },
	} {
		dir := writeTestBundle(t, "A0")
		path := filepath.Join(dir, ManifestFileName)
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var value manifest
		if err := json.Unmarshal(payload, &value); err != nil {
			t.Fatal(err)
		}
		mutate(&value)
		payload, err = renderJSON(value)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, path, payload)
		report := VerifyDirectory(dir)
		if report.Status != "fail" {
			t.Fatalf("manifest mutation passed: %#v", report)
		}
	}
}

func TestVerifyDirectoryRejectsReorderedManifestEntries(t *testing.T) {
	dir := writeTestBundle(t, "A0")
	path := filepath.Join(dir, ManifestFileName)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value manifest
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	value.Files[0], value.Files[1] = value.Files[1], value.Files[0]
	payload, err = renderJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, payload)
	requireFailedCheck(t, VerifyDirectory(dir), "bundle.manifest.invalid")
}

func TestVerifyDirectoryRejectsStrictManifestUnknownAndDuplicateFields(t *testing.T) {
	for _, mutate := range []func([]byte) []byte{
		func(payload []byte) []byte {
			return []byte(strings.Replace(string(payload), `"schema_version":`, `"unknown": true, "schema_version":`, 1))
		},
		func(payload []byte) []byte {
			return []byte(strings.Replace(string(payload), `"schema_version":`, `"schema_version": "fabricctl.bundle-manifest/v1", "schema_version":`, 1))
		},
	} {
		dir := writeTestBundle(t, "A0")
		path := filepath.Join(dir, ManifestFileName)
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, path, mutate(payload))
		requireFailedCheck(t, VerifyDirectory(dir), "bundle.manifest.invalid")
	}
}

func TestVerifyDirectoryRejectsNoncanonicalManifestAndPlan(t *testing.T) {
	for _, name := range []string{ManifestFileName, InstallationPlanFileName} {
		t.Run(name, func(t *testing.T) {
			dir := writeTestBundle(t, "A0")
			path := filepath.Join(dir, name)
			payload, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, path, append([]byte(" \n"), payload...))
			if name != ManifestFileName {
				rewriteManifest(t, dir)
			}
			report := VerifyDirectory(dir)
			if report.Status != "fail" || len(report.Diagnostics) != 1 {
				t.Fatalf("noncanonical artifact passed: %#v", report)
			}
		})
	}
}

func TestVerifyDirectoryRejectsRehashedStalePlan(t *testing.T) {
	dir := writeTestBundle(t, "A0")
	path := filepath.Join(dir, InstallationPlanFileName)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var plan installationPlan
	if err := json.Unmarshal(payload, &plan); err != nil {
		t.Fatal(err)
	}
	plan.Deployment.Digest = "sha256:" + strings.Repeat("9", 64)
	payload, err = renderJSON(plan)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, payload)
	rewriteManifest(t, dir)
	requireFailedCheck(t, VerifyDirectory(dir), "bundle.plan.stale")
}

func TestVerifyDirectoryRejectsRehashedStaleValues(t *testing.T) {
	dir := writeTestBundle(t, "A0")
	path := filepath.Join(dir, ValuesFileName)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, append(payload, []byte("operatorAdded: true\n")...))
	rewriteManifest(t, dir)
	requireFailedCheck(t, VerifyDirectory(dir), "bundle.values.stale")
}

func TestVerifyDirectoryRejectsRehashedNoncanonicalSources(t *testing.T) {
	for _, name := range []string{DeploymentFileName, InstallTargetFileName} {
		t.Run(name, func(t *testing.T) {
			dir := writeTestBundle(t, "A0")
			path := filepath.Join(dir, name)
			payload, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, path, append([]byte("---\n"), payload...))
			rewriteManifest(t, dir)
			report := VerifyDirectory(dir)
			if report.Status != "fail" || len(report.Diagnostics) != 1 || !strings.Contains(report.Diagnostics[0].ID, "noncanonical") {
				t.Fatalf("noncanonical source passed: %#v", report)
			}
		})
	}
}

func TestVerifyDirectoryRejectsRehashedSecretValueInjection(t *testing.T) {
	dir := writeTestBundle(t, "A3")
	secretSentinel := "do-not-echo-this-sensitive-value"
	path := filepath.Join(dir, SecretsRequiredFileName)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, []byte("data:\n  token: "+secretSentinel+"\n")...)
	mustWrite(t, path, payload)
	rewriteManifest(t, dir)
	report := VerifyDirectory(dir)
	requireFailedCheck(t, report, "bundle.secret_requirements.invalid")
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secretSentinel) || strings.Contains(string(encoded), dir) {
		t.Fatalf("report leaked input path or value: %s", encoded)
	}
}

func TestVerifyDirectoryNeverEchoesSelectedPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sensitive-customer-name")
	report := VerifyDirectory(dir)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), dir) || strings.Contains(string(encoded), "sensitive-customer-name") {
		t.Fatalf("report leaked selected path: %s", encoded)
	}
}

func mustWrite(t *testing.T, path string, payload []byte) {
	t.Helper()
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}
