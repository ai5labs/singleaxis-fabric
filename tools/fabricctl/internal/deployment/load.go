// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func documentError(id, summary string) *DocumentError {
	return &DocumentError{Diagnostic: Diagnostic{ID: id, Severity: "error", Path: "$", Summary: summary}}
}

var errNotRegularFile = errors.New("deployment input is not a regular file")

// LoadFile reads a bounded YAML or JSON deployment document. The bounded
// reader protects against a file growing between stat and read.
func LoadFile(path string) (any, error) {
	f, err := openRegularFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, documentError("deployment.file.not_found", "Deployment file was not found")
	}
	if errors.Is(err, errNotRegularFile) {
		return nil, documentError("deployment.file.not_regular", "Deployment path must identify one regular file")
	}
	if err != nil {
		return nil, documentError("deployment.file.unreadable", "Deployment file cannot be read")
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, documentError("deployment.file.unreadable", "Deployment file cannot be read")
	}
	if info.Size() > MaxDocumentBytes {
		return nil, documentError("deployment.file.too_large", fmt.Sprintf("Deployment file exceeds the %d-byte limit", MaxDocumentBytes))
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil {
		return nil, documentError("deployment.file.unreadable", "Deployment file cannot be read")
	}
	if len(raw) > MaxDocumentBytes {
		return nil, documentError("deployment.file.too_large", fmt.Sprintf("Deployment file exceeds the %d-byte limit", MaxDocumentBytes))
	}
	return LoadBytes(raw, filepath.Ext(path))
}

// LoadBytes safely decodes one document. format may be ".json", "json",
// ".yaml", or "yaml"; every non-JSON value is treated as YAML.
func LoadBytes(raw []byte, format string) (any, error) {
	if len(raw) > MaxDocumentBytes {
		return nil, documentError("deployment.file.too_large", fmt.Sprintf("Deployment file exceeds the %d-byte limit", MaxDocumentBytes))
	}
	if !utf8.Valid(raw) {
		return nil, documentError("deployment.document.encoding", "Deployment file must be UTF-8")
	}
	if format == ".json" || format == "json" {
		value, err := decodeJSON(raw)
		if err != nil {
			return nil, documentError("deployment.document.syntax", "Deployment file contains invalid or duplicate syntax")
		}
		return value, nil
	}
	value, err := decodeYAML(raw)
	if err != nil {
		var aliasErr *aliasForbiddenError
		if errors.As(err, &aliasErr) {
			return nil, documentError("deployment.document.alias_forbidden", "YAML anchors and aliases are forbidden in FabricDeployment files")
		}
		return nil, documentError("deployment.document.syntax", "Deployment file contains invalid or duplicate syntax")
	}
	return value, nil
}

// ParseFile loads and validates while retaining the generic decoded document.
func ParseFile(path string) (*Parsed, []Diagnostic, error) {
	value, err := LoadFile(path)
	if err != nil {
		return nil, nil, err
	}
	resource, diagnostics := Validate(value)
	if len(diagnostics) != 0 {
		return nil, diagnostics, nil
	}
	return &Parsed{Document: value, Resource: *resource}, nil, nil
}

func decodeJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := readJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func readJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("non-string JSON key")
			}
			if _, exists := object[key]; exists {
				return nil, errors.New("duplicate JSON key")
			}
			value, err := readJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errors.New("unterminated JSON object")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := readJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errors.New("unterminated JSON array")
		}
		return array, nil
	default:
		return nil, errors.New("unexpected JSON delimiter")
	}
}

type aliasForbiddenError struct{}

func (*aliasForbiddenError) Error() string { return "YAML alias or anchor is forbidden" }

func decodeYAML(raw []byte) (any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(false) // Contract validation, not YAML, owns field diagnostics.
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return nil, errors.New("empty YAML document")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple YAML documents")
		}
		return nil, err
	}
	if err := inspectYAMLNode(&document); err != nil {
		return nil, err
	}
	var value any
	if err := document.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

var allowedYAMLTags = map[string]bool{
	"!!map": true, "!!seq": true, "!!str": true, "!!null": true,
	"!!bool": true, "!!int": true, "!!float": true,
}

func inspectYAMLNode(node *yaml.Node) error {
	if node.Anchor != "" || node.Kind == yaml.AliasNode || node.Alias != nil {
		return &aliasForbiddenError{}
	}
	if node.Tag != "" && !allowedYAMLTags[node.ShortTag()] {
		return errors.New("unsupported YAML tag")
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.ShortTag() != "!!str" || key.Value == "<<" {
				return errors.New("YAML mapping keys must be strings and merge keys are forbidden")
			}
			if seen[key.Value] {
				return errors.New("duplicate YAML key")
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := inspectYAMLNode(child); err != nil {
			return err
		}
	}
	return nil
}
