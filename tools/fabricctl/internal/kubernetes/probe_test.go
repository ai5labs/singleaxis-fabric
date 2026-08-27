// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSyntheticTracePayloadContainsOnlyGeneratedMetadata(t *testing.T) {
	id, payload, err := syntheticTracePayload()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, "synthetic/") || len(id) != len("synthetic/")+32 {
		t.Fatalf("invalid synthetic id: %q", id)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{"prompt", "response", "tool_payload", "authorization", "token"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("synthetic payload contains forbidden class %q: %s", forbidden, text)
		}
	}
}
