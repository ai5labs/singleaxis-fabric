// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

// Package artifacts validates release artifacts that are resolved after the
// deterministic desired-state bundle and bound into a mutation plan.
package artifacts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
	"gopkg.in/yaml.v3"
)

const ImageLocksSchema = "fabricctl.image-locks/v1"

var (
	imageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	imageRepoPattern   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._/-]*[A-Za-z0-9])?$`)
	releasePattern     = regexp.MustCompile(`^v?(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
)

var supportedComponents = map[string]bool{
	"otel-collector":     true,
	"otel-redactor":      true,
	"nemo-sidecar":       true,
	"fabric-relay":       true,
	"presidio-sidecar":   true,
	"langfuse":           true,
	"langfuse-bootstrap": true,
	"redteam-runner":     true,
	"update-agent":       true,
}

type ImageLockFile struct {
	SchemaVersion string      `json:"schema_version"`
	Release       string      `json:"release"`
	Images        []ImageLock `json:"images"`
}

type ImageLock struct {
	Component  string `json:"component"`
	Repository string `json:"repository"`
	Digest     string `json:"digest"`
}

// ParseImageLocks strictly decodes and validates one release image lock file.
// It accepts JSON or YAML but returns only stable, value-free errors.
func ParseImageLocks(payload []byte, format string) (ImageLockFile, error) {
	document, err := deployment.LoadBytes(payload, format)
	if err != nil {
		return ImageLockFile{}, fmt.Errorf("image lock document cannot be safely decoded")
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return ImageLockFile{}, fmt.Errorf("image lock document cannot be normalized")
	}
	var result ImageLockFile
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return ImageLockFile{}, fmt.Errorf("image lock document does not satisfy the strict contract")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ImageLockFile{}, fmt.Errorf("image lock document contains trailing content")
	}
	if err := validateImageLocks(result); err != nil {
		return ImageLockFile{}, err
	}
	sort.Slice(result.Images, func(i, j int) bool { return result.Images[i].Component < result.Images[j].Component })
	return result, nil
}

func validateImageLocks(value ImageLockFile) error {
	if value.SchemaVersion != ImageLocksSchema || !releasePattern.MatchString(value.Release) || len(value.Images) == 0 || len(value.Images) > len(supportedComponents) {
		return fmt.Errorf("image lock document has invalid release identity or image count")
	}
	seen := make(map[string]bool, len(value.Images))
	for _, image := range value.Images {
		if !supportedComponents[image.Component] || seen[image.Component] || !validRepository(image.Repository) || !imageDigestPattern.MatchString(image.Digest) {
			return fmt.Errorf("image lock entry is unsupported, duplicated, mutable, or malformed")
		}
		seen[image.Component] = true
	}
	return nil
}

func validRepository(value string) bool {
	if len(value) == 0 || len(value) > 512 || !imageRepoPattern.MatchString(value) || strings.Contains(value, "@") || strings.Contains(value, "://") {
		return false
	}
	lastSlash := strings.LastIndex(value, "/")
	return !strings.Contains(value[lastSlash+1:], ":")
}

// RenderHelmValues produces the only supported image override shape. The
// rendered manifest must still be inspected for complete immutable coverage.
func RenderHelmValues(value ImageLockFile) ([]byte, error) {
	result := make(map[string]any)
	for _, image := range value.Images {
		locked := map[string]any{"repository": image.Repository, "digest": image.Digest}
		switch image.Component {
		case "otel-collector":
			setNested(result, []string{"otel-collector", "image"}, locked)
		case "otel-redactor":
			setNested(result, []string{"otel-collector", "fabric", "redact", "embedded", "image"}, locked)
		case "nemo-sidecar":
			setNested(result, []string{"nemo-sidecar", "image"}, locked)
		case "fabric-relay":
			setNested(result, []string{"fabric-relay", "image"}, locked)
		case "presidio-sidecar":
			setNested(result, []string{"presidio-sidecar", "image"}, locked)
		case "langfuse":
			setNested(result, []string{"langfuse", "image"}, locked)
		case "langfuse-bootstrap":
			setNested(result, []string{"langfuse", "bootstrap", "image"}, locked)
		case "redteam-runner":
			setNested(result, []string{"redteam-runner", "image"}, locked)
		case "update-agent":
			setNested(result, []string{"update-agent", "image"}, locked)
		}
	}
	return yaml.Marshal(result)
}

func setNested(root map[string]any, path []string, value any) {
	cursor := root
	for _, key := range path[:len(path)-1] {
		next, ok := cursor[key].(map[string]any)
		if !ok {
			next = make(map[string]any)
			cursor[key] = next
		}
		cursor = next
	}
	cursor[path[len(path)-1]] = value
}
