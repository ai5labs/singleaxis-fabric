// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package artifacts

import (
	"strings"
	"testing"
)

func imageLocksJSON() string {
	return `{"schema_version":"fabricctl.image-locks/v1","release":"0.7.1","images":[` +
		`{"component":"nemo-sidecar","repository":"ghcr.io/singleaxis/fabric-nemo-sidecar","digest":"sha256:` + strings.Repeat("2", 64) + `"},` +
		`{"component":"otel-collector","repository":"ghcr.io/singleaxis/fabric-otelcol","digest":"sha256:` + strings.Repeat("1", 64) + `"}]}`
}

func TestParseAndRenderImageLocks(t *testing.T) {
	locks, err := ParseImageLocks([]byte(imageLocksJSON()), "json")
	if err != nil {
		t.Fatal(err)
	}
	if locks.Images[0].Component != "nemo-sidecar" {
		t.Fatalf("locks are not canonical: %#v", locks.Images)
	}
	payload, err := RenderHelmValues(locks)
	if err != nil {
		t.Fatal(err)
	}
	output := string(payload)
	for _, expected := range []string{"nemo-sidecar:", "otel-collector:", "digest: sha256:"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("render missing %q: %s", expected, output)
		}
	}
}

func TestImageLocksRejectMutableDuplicateUnknownAndCredentialLikeInputs(t *testing.T) {
	mutations := []string{
		strings.Replace(imageLocksJSON(), `sha256:`+strings.Repeat("1", 64), "latest", 1),
		strings.Replace(imageLocksJSON(), `"component":"nemo-sidecar"`, `"component":"otel-collector"`, 1),
		strings.Replace(imageLocksJSON(), `"component":"nemo-sidecar"`, `"component":"unknown"`, 1),
		strings.Replace(imageLocksJSON(), "ghcr.io/singleaxis/fabric-nemo-sidecar", "user:password@example/image", 1),
	}
	for _, payload := range mutations {
		if _, err := ParseImageLocks([]byte(payload), "json"); err == nil {
			t.Fatalf("unsafe lock passed: %s", payload)
		}
	}
}
