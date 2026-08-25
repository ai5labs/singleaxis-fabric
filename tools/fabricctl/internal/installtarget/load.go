// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package installtarget

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var errNotRegularFile = errors.New("install-target input is not a regular file")

func documentError(id, summary string) *DocumentError {
	return &DocumentError{Diagnostic: Diagnostic{ID: id, Severity: "error", Path: "$", Summary: summary}}
}

// LoadFile reads one bounded regular YAML or JSON file without following a
// final symlink. Diagnostics never include the selected path.
func LoadFile(path string) (any, error) {
	file, err := openRegularFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, documentError("installtarget.file.not_found", "Install target file was not found")
	}
	if errors.Is(err, errNotRegularFile) {
		return nil, documentError("installtarget.file.not_regular", "Install target path must identify one regular file")
	}
	if err != nil {
		return nil, documentError("installtarget.file.unreadable", "Install target file cannot be read")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, documentError("installtarget.file.unreadable", "Install target file cannot be read")
	}
	if info.Size() > MaxDocumentBytes {
		return nil, tooLargeError()
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxDocumentBytes+1))
	if err != nil {
		return nil, documentError("installtarget.file.unreadable", "Install target file cannot be read")
	}
	if len(raw) > MaxDocumentBytes {
		return nil, tooLargeError()
	}
	return LoadBytes(raw, filepath.Ext(path))
}

func tooLargeError() *DocumentError {
	return documentError("installtarget.file.too_large", fmt.Sprintf("Install target file exceeds the %d-byte limit", MaxDocumentBytes))
}

// LoadBytes safely decodes exactly one YAML or JSON document. format accepts
// json/.json and yaml/.yaml/.yml; an unsupported value fails closed.
func LoadBytes(raw []byte, format string) (any, error) {
	if len(raw) > MaxDocumentBytes {
		return nil, tooLargeError()
	}
	if !utf8.Valid(raw) {
		return nil, documentError("installtarget.document.encoding", "Install target file must be UTF-8")
	}
	switch strings.ToLower(format) {
	case ".json", "json":
		value, err := decodeJSON(raw)
		if err != nil {
			return nil, documentError("installtarget.document.syntax", "Install target file contains invalid or duplicate syntax")
		}
		return value, nil
	case ".yaml", "yaml", ".yml", "yml", "":
		value, err := decodeYAML(raw)
		if err != nil {
			var forbidden *aliasForbiddenError
			if errors.As(err, &forbidden) {
				return nil, documentError("installtarget.document.alias_forbidden", "YAML anchors and aliases are forbidden in FabricInstallTarget files")
			}
			return nil, documentError("installtarget.document.syntax", "Install target file contains invalid or duplicate syntax")
		}
		return value, nil
	default:
		return nil, documentError("installtarget.document.format", "Install target file format must be JSON or YAML")
	}
}

// ParseFile loads and validates an install target while retaining its generic
// representation for review-identity calculation.
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
	delimiter, ok := token.(json.Delim)
	if !ok {
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
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Kind != yaml.ScalarNode || key.ShortTag() != "!!str" || key.Value == "<<" {
				return errors.New("invalid YAML mapping key")
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
