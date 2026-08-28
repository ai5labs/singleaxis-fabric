// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package deployment

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	namePattern                 = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,61}[a-z0-9])?$`)
	referencePattern            = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._:/-]{0,251}[A-Za-z0-9])?$`)
	opaqueReferencePattern      = regexp.MustCompile(`^[A-Za-z0-9_-]{48,}$`)
	hexReferencePattern         = regexp.MustCompile(`^[A-Fa-f0-9]{40,}$`)
	credentialReferencePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^(?:bearer[ :]?)[A-Za-z0-9._~+/-]+$`),
		regexp.MustCompile(`(?i)^(?:sk|pk|api[_-]?key|token|secret)[_-][A-Za-z0-9._~+/-]{8,}$`),
		regexp.MustCompile(`^(?:AKIA|ASIA)[A-Z0-9]{16}$`),
		regexp.MustCompile(`^gh[pousr]_[A-Za-z0-9]{20,}$`),
		regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`),
	}
	forbiddenKeys = map[string]bool{
		"env": true, "envs": true, "envvars": true, "environmentvariables": true,
		"password": true, "passwd": true, "secret": true, "token": true,
		"apikey": true, "api_key": true, "credential": true, "credentials": true,
	}
)

func diagnostic(id, path, summary string) Diagnostic {
	return Diagnostic{ID: id, Severity: "error", Path: path, Summary: summary}
}

// Validate applies the complete strict v1alpha1 contract. It scans for inline
// sensitive keys before producing any structural diagnostic.
func Validate(value any) (*Resource, []Diagnostic) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, []Diagnostic{diagnostic("deployment.document.type", "$", "Deployment document must be a mapping")}
	}
	if security := findForbiddenKeys(object, "$"); len(security) != 0 {
		return nil, security
	}

	var diagnostics []Diagnostic
	rejectUnknown(object, "$", []string{"apiVersion", "kind", "metadata", "spec"}, &diagnostics)
	apiVersion := requiredString(object, "apiVersion", "$", &diagnostics)
	kind := requiredString(object, "kind", "$", &diagnostics)
	metadataMap := requiredObject(object, "metadata", "$", &diagnostics)
	specMap := requiredObject(object, "spec", "$", &diagnostics)
	if apiVersion != "" && apiVersion != APIVersion {
		diagnostics = append(diagnostics, fieldValue("$.apiVersion"))
	}
	if kind != "" && kind != Kind {
		diagnostics = append(diagnostics, fieldValue("$.kind"))
	}

	resource := Resource{APIVersion: apiVersion, Kind: kind}
	if metadataMap != nil {
		rejectUnknown(metadataMap, "$.metadata", []string{"name"}, &diagnostics)
		resource.Metadata.Name = requiredString(metadataMap, "name", "$.metadata", &diagnostics)
	}
	if specMap != nil {
		validateSpec(specMap, &resource.Spec, &diagnostics)
	}
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}

	if !namePattern.MatchString(resource.Metadata.Name) {
		diagnostics = append(diagnostics, diagnostic(
			"deployment.identity.invalid_name", "$.metadata.name",
			"metadata.name must be a lowercase DNS-style name of at most 63 characters",
		))
	}
	for _, reference := range rawReferenceValues(object) {
		if !referencePattern.MatchString(reference.value) {
			diagnostics = append(diagnostics, diagnostic(
				"deployment.reference.invalid", "$."+reference.field,
				"Reference must be 1-253 identifier characters and contain no inline value",
			))
		} else if ReferenceLooksSensitive(reference.value) {
			diagnostics = append(diagnostics, diagnostic(
				"deployment.reference.sensitive", "$."+reference.field,
				"Reference resembles credential material; use an external reference identifier instead",
			))
		}
	}
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}
	return &resource, nil
}

// ReferenceLooksSensitive rejects common credential formats and long opaque
// tokens. It is intentionally shared by declarative validation and the wizard
// so automation cannot bypass the interactive safety check.
func ReferenceLooksSensitive(value string) bool {
	for _, pattern := range credentialReferencePatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return !strings.ContainsAny(value, "/:.") &&
		(opaqueReferencePattern.MatchString(value) || hexReferencePattern.MatchString(value))
}

func validateSpec(object map[string]any, spec *Spec, diagnostics *[]Diagnostic) {
	rejectUnknown(object, "$.spec", []string{"assuranceLevel", "connection", "controls", "observe", "assurance", "rollout"}, diagnostics)
	spec.AssuranceLevel = requiredString(object, "assuranceLevel", "$.spec", diagnostics)
	if spec.AssuranceLevel != "" && !oneOf(spec.AssuranceLevel, "A0", "A1", "A2", "A3") {
		*diagnostics = append(*diagnostics, fieldValue("$.spec.assuranceLevel"))
	}

	connection := requiredObject(object, "connection", "$.spec", diagnostics)
	if connection != nil {
		rejectUnknown(connection, "$.spec.connection", []string{"mode", "tenantIdFrom", "workloadIdentityRef"}, diagnostics)
		spec.Connection.Mode = requiredString(connection, "mode", "$.spec.connection", diagnostics)
		if spec.Connection.Mode != "" && !oneOf(spec.Connection.Mode, "sdk", "adapter", "gateway", "otlp") {
			*diagnostics = append(*diagnostics, fieldValue("$.spec.connection.mode"))
		}
		spec.Connection.TenantIDFrom = requiredString(connection, "tenantIdFrom", "$.spec.connection", diagnostics)
		spec.Connection.WorkloadIdentityRef = optionalString(connection, "workloadIdentityRef", "$.spec.connection", diagnostics)
	}

	observe := requiredObject(object, "observe", "$.spec", diagnostics)
	if observe != nil {
		rejectUnknown(observe, "$.spec.observe", []string{"contentMode", "relayRef"}, diagnostics)
		spec.Observe.ContentMode = requiredString(observe, "contentMode", "$.spec.observe", diagnostics)
		if spec.Observe.ContentMode != "" && !oneOf(spec.Observe.ContentMode, "metadata-only", "hash-only", "content-ref") {
			*diagnostics = append(*diagnostics, fieldValue("$.spec.observe.contentMode"))
		}
		spec.Observe.RelayRef = optionalString(observe, "relayRef", "$.spec.observe", diagnostics)
	}

	if controls, present := optionalObject(object, "controls", "$.spec", diagnostics); present && controls != nil {
		rejectUnknown(controls, "$.spec.controls", []string{"profileRef", "policyRef", "authorizationRef", "piiRef", "guardrailRef", "escalationRef"}, diagnostics)
		spec.Controls = &Controls{
			ProfileRef:       requiredString(controls, "profileRef", "$.spec.controls", diagnostics),
			PolicyRef:        optionalString(controls, "policyRef", "$.spec.controls", diagnostics),
			AuthorizationRef: optionalString(controls, "authorizationRef", "$.spec.controls", diagnostics),
			PIIRef:           optionalString(controls, "piiRef", "$.spec.controls", diagnostics),
			GuardrailRef:     optionalString(controls, "guardrailRef", "$.spec.controls", diagnostics),
			EscalationRef:    optionalString(controls, "escalationRef", "$.spec.controls", diagnostics),
		}
	}
	if assurance, present := optionalObject(object, "assurance", "$.spec", diagnostics); present && assurance != nil {
		rejectUnknown(assurance, "$.spec.assurance", []string{"planRef"}, diagnostics)
		spec.Assurance = &Assurance{PlanRef: requiredString(assurance, "planRef", "$.spec.assurance", diagnostics)}
	}
	if rollout, present := optionalObject(object, "rollout", "$.spec", diagnostics); present && rollout != nil {
		rejectUnknown(rollout, "$.spec.rollout", []string{"approvalRef"}, diagnostics)
		spec.Rollout = &Rollout{ApprovalRef: requiredString(rollout, "approvalRef", "$.spec.rollout", diagnostics)}
	}

	// Match the public runtime's single stable cross-field diagnostic. Do not
	// duplicate it when basic shape errors already make the level unknowable.
	if !oneOf(spec.AssuranceLevel, "A0", "A1", "A2", "A3") {
		return
	}
	assuranceInvalid := false
	if spec.AssuranceLevel != "A0" && spec.Observe.RelayRef == "" {
		assuranceInvalid = true
	}
	if (spec.AssuranceLevel == "A2" || spec.AssuranceLevel == "A3") &&
		(spec.Controls == nil || spec.Assurance == nil || spec.Rollout == nil) {
		assuranceInvalid = true
	}
	if spec.AssuranceLevel == "A3" && spec.Connection.WorkloadIdentityRef == "" {
		assuranceInvalid = true
	}
	if assuranceInvalid {
		*diagnostics = append(*diagnostics, diagnostic(
			"deployment.assurance.requirements", "$.spec", "Assurance level requirements are not satisfied",
		))
	}
}

func findForbiddenKeys(value any, path string) []Diagnostic {
	var diagnostics []Diagnostic
	switch typed := value.(type) {
	case map[string]any:
		keys := sortedKeys(typed)
		for _, key := range keys {
			childPath := path + "." + key
			if forbiddenKeys[strings.ToLower(key)] {
				diagnostics = append(diagnostics, diagnostic(
					"deployment.security.inline_sensitive_value", childPath,
					"Inline secrets, credentials, tokens, and environment dumps are forbidden",
				))
			}
			diagnostics = append(diagnostics, findForbiddenKeys(typed[key], childPath)...)
		}
	case []any:
		for index, child := range typed {
			diagnostics = append(diagnostics, findForbiddenKeys(child, fmt.Sprintf("%s[%d]", path, index))...)
		}
	}
	return diagnostics
}

func rejectUnknown(object map[string]any, path string, allowed []string, diagnostics *[]Diagnostic) {
	allow := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allow[key] = true
	}
	for _, key := range sortedKeys(object) {
		if !allow[key] {
			*diagnostics = append(*diagnostics, diagnostic("deployment.field.unknown", path+"."+key, "Unknown deployment field is forbidden"))
		}
	}
}

func requiredString(object map[string]any, key, path string, diagnostics *[]Diagnostic) string {
	value, exists := object[key]
	if !exists {
		*diagnostics = append(*diagnostics, diagnostic("deployment.field.required", path+"."+key, "Required deployment field is missing"))
		return ""
	}
	text, ok := value.(string)
	if !ok {
		*diagnostics = append(*diagnostics, diagnostic("deployment.field.type", path+"."+key, "Deployment field has the wrong type"))
		return ""
	}
	return text
}

func optionalString(object map[string]any, key, path string, diagnostics *[]Diagnostic) string {
	value, exists := object[key]
	if !exists {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		*diagnostics = append(*diagnostics, diagnostic("deployment.field.type", path+"."+key, "Deployment field has the wrong type"))
		return ""
	}
	return text
}

func requiredObject(object map[string]any, key, path string, diagnostics *[]Diagnostic) map[string]any {
	value, exists := object[key]
	if !exists {
		*diagnostics = append(*diagnostics, diagnostic("deployment.field.required", path+"."+key, "Required deployment field is missing"))
		return nil
	}
	typed, ok := value.(map[string]any)
	if !ok {
		*diagnostics = append(*diagnostics, diagnostic("deployment.field.type", path+"."+key, "Deployment field has the wrong type"))
		return nil
	}
	return typed
}

func optionalObject(object map[string]any, key, path string, diagnostics *[]Diagnostic) (map[string]any, bool) {
	value, exists := object[key]
	if !exists {
		return nil, false
	}
	typed, ok := value.(map[string]any)
	if !ok {
		*diagnostics = append(*diagnostics, diagnostic("deployment.field.type", path+"."+key, "Deployment field has the wrong type"))
		return nil, true
	}
	return typed, true
}

func fieldValue(path string) Diagnostic {
	return diagnostic("deployment.field.value", path, "Deployment field has an unsupported value")
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type referenceValue struct{ field, value string }

// rawReferenceValues preserves the distinction between an omitted optional
// reference and a present empty reference. The typed model intentionally uses
// plain strings for ergonomic callers, so validation must inspect the source
// object for this JSON Schema minLength rule.
func rawReferenceValues(root map[string]any) []referenceValue {
	spec, _ := root["spec"].(map[string]any)
	connection, _ := spec["connection"].(map[string]any)
	observe, _ := spec["observe"].(map[string]any)
	controls, _ := spec["controls"].(map[string]any)
	assurance, _ := spec["assurance"].(map[string]any)
	rollout, _ := spec["rollout"].(map[string]any)

	var result []referenceValue
	appendRawReference := func(object map[string]any, key, field string) {
		if object == nil {
			return
		}
		value, present := object[key]
		if !present {
			return
		}
		if text, ok := value.(string); ok {
			result = append(result, referenceValue{field: field, value: text})
		}
	}
	appendRawReference(connection, "tenantIdFrom", "spec.connection.tenantIdFrom")
	appendRawReference(connection, "workloadIdentityRef", "spec.connection.workloadIdentityRef")
	appendRawReference(observe, "relayRef", "spec.observe.relayRef")
	appendRawReference(controls, "profileRef", "spec.controls.profileRef")
	appendRawReference(controls, "policyRef", "spec.controls.policyRef")
	appendRawReference(controls, "authorizationRef", "spec.controls.authorizationRef")
	appendRawReference(controls, "piiRef", "spec.controls.piiRef")
	appendRawReference(controls, "guardrailRef", "spec.controls.guardrailRef")
	appendRawReference(controls, "escalationRef", "spec.controls.escalationRef")
	appendRawReference(assurance, "planRef", "spec.assurance.planRef")
	appendRawReference(rollout, "approvalRef", "spec.rollout.approvalRef")
	return result
}
