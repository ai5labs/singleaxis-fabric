// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package installtarget

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
)

var (
	namePattern      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,61}[a-z0-9])?$`)
	namespacePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	referencePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._:/-]{0,251}[A-Za-z0-9])?$`)
	versionPattern   = regexp.MustCompile(`^v?(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	digestPattern    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	dnsHostPattern   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	artifactPattern  = regexp.MustCompile(`^oci://[A-Za-z0-9](?:[A-Za-z0-9._:/-]*[A-Za-z0-9])?$`)
	endpointPattern  = regexp.MustCompile(`^https://[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?(?::[0-9]{1,5})?(?:/[A-Za-z0-9._~!$&'()*+,;=:@%/-]*)?$`)
	forbiddenKeys    = map[string]bool{
		"env": true, "envs": true, "envvars": true, "environmentvariables": true,
		"password": true, "passwd": true, "secret": true, "token": true,
		"apikey": true, "credential": true, "credentials": true,
	}
)

func diagnostic(id, path, summary string) Diagnostic {
	return Diagnostic{ID: id, Severity: "error", Path: path, Summary: summary}
}

// Validate applies the complete strict FabricInstallTarget v1alpha1 contract.
func Validate(value any) (*Resource, []Diagnostic) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, []Diagnostic{diagnostic("installtarget.document.type", "$", "Install target document must be a mapping")}
	}
	if sensitive := findForbiddenKeys(object); len(sensitive) != 0 {
		return nil, sensitive
	}

	var diagnostics []Diagnostic
	rejectUnknown(object, "$", []string{"apiVersion", "kind", "metadata", "spec"}, &diagnostics)
	resource := Resource{
		APIVersion: requiredString(object, "apiVersion", "$", &diagnostics),
		Kind:       requiredString(object, "kind", "$", &diagnostics),
	}
	metadata := requiredObject(object, "metadata", "$", &diagnostics)
	if metadata != nil {
		rejectUnknown(metadata, "$.metadata", []string{"name"}, &diagnostics)
		resource.Metadata.Name = requiredString(metadata, "name", "$.metadata", &diagnostics)
	}
	spec := requiredObject(object, "spec", "$", &diagnostics)
	if spec != nil {
		parseSpec(spec, &resource.Spec, &diagnostics)
	}
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}
	diagnostics = validateTyped(resource)
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}
	return &resource, nil
}

func parseSpec(object map[string]any, spec *Spec, diagnostics *[]Diagnostic) {
	rejectUnknown(object, "$.spec", []string{"deploymentRef", "distribution", "profile", "backend", "bindings"}, diagnostics)
	deploymentRef := requiredObject(object, "deploymentRef", "$.spec", diagnostics)
	if deploymentRef != nil {
		rejectUnknown(deploymentRef, "$.spec.deploymentRef", []string{"name", "digest"}, diagnostics)
		spec.DeploymentRef = DeploymentRef{
			Name:   requiredString(deploymentRef, "name", "$.spec.deploymentRef", diagnostics),
			Digest: requiredString(deploymentRef, "digest", "$.spec.deploymentRef", diagnostics),
		}
	}
	distribution := requiredObject(object, "distribution", "$.spec", diagnostics)
	if distribution != nil {
		rejectUnknown(distribution, "$.spec.distribution", []string{"artifactRef", "version", "digest"}, diagnostics)
		spec.Distribution = Distribution{
			ArtifactRef: requiredString(distribution, "artifactRef", "$.spec.distribution", diagnostics),
			Version:     requiredString(distribution, "version", "$.spec.distribution", diagnostics),
			Digest:      requiredString(distribution, "digest", "$.spec.distribution", diagnostics),
		}
	}
	profile := requiredObject(object, "profile", "$.spec", diagnostics)
	if profile != nil {
		rejectUnknown(profile, "$.spec.profile", []string{"name", "digest"}, diagnostics)
		spec.Profile = Profile{
			Name:   requiredString(profile, "name", "$.spec.profile", diagnostics),
			Digest: requiredString(profile, "digest", "$.spec.profile", diagnostics),
		}
	}
	backend := requiredObject(object, "backend", "$.spec", diagnostics)
	if backend != nil {
		rejectUnknown(backend, "$.spec.backend", []string{"type", "helm"}, diagnostics)
		spec.Backend.Type = requiredString(backend, "type", "$.spec.backend", diagnostics)
		helm := requiredObject(backend, "helm", "$.spec.backend", diagnostics)
		if helm != nil {
			rejectUnknown(helm, "$.spec.backend.helm", []string{"context", "namespace", "releaseName", "createNamespace"}, diagnostics)
			spec.Backend.Helm = HelmTarget{
				Context:         requiredString(helm, "context", "$.spec.backend.helm", diagnostics),
				Namespace:       requiredString(helm, "namespace", "$.spec.backend.helm", diagnostics),
				ReleaseName:     requiredString(helm, "releaseName", "$.spec.backend.helm", diagnostics),
				CreateNamespace: requiredBool(helm, "createNamespace", "$.spec.backend.helm", diagnostics),
			}
		}
	}
	if bindings, present := optionalObject(object, "bindings", "$.spec", diagnostics); present && bindings != nil {
		spec.Bindings = parseBindings(bindings, diagnostics)
	}
}

func parseBindings(object map[string]any, diagnostics *[]Diagnostic) *Bindings {
	rejectUnknown(object, "$.spec.bindings", []string{"tenantId", "exporter", "updateTrust"}, diagnostics)
	result := &Bindings{TenantID: requiredString(object, "tenantId", "$.spec.bindings", diagnostics)}
	exporter := requiredObject(object, "exporter", "$.spec.bindings", diagnostics)
	if exporter != nil {
		rejectUnknown(exporter, "$.spec.bindings.exporter", []string{"endpoint", "egress"}, diagnostics)
		result.Exporter.Endpoint = requiredString(exporter, "endpoint", "$.spec.bindings.exporter", diagnostics)
		egress := requiredObject(exporter, "egress", "$.spec.bindings.exporter", diagnostics)
		if egress != nil {
			rejectUnknown(egress, "$.spec.bindings.exporter.egress", []string{"cidrs", "ports"}, diagnostics)
			result.Exporter.Egress.CIDRs = requiredStringArray(egress, "cidrs", "$.spec.bindings.exporter.egress", diagnostics)
			result.Exporter.Egress.Ports = requiredPorts(egress, "ports", "$.spec.bindings.exporter.egress", diagnostics)
		}
	}
	trust := requiredObject(object, "updateTrust", "$.spec.bindings", diagnostics)
	if trust != nil {
		rejectUnknown(trust, "$.spec.bindings.updateTrust", []string{"keyId", "publicKey"}, diagnostics)
		result.UpdateTrust = UpdateTrust{
			KeyID:     requiredString(trust, "keyId", "$.spec.bindings.updateTrust", diagnostics),
			PublicKey: requiredString(trust, "publicKey", "$.spec.bindings.updateTrust", diagnostics),
		}
	}
	return result
}

func requiredPorts(object map[string]any, key, path string, diagnostics *[]Diagnostic) []Port {
	value, exists := object[key]
	if !exists {
		*diagnostics = append(*diagnostics, requiredField(path+"."+key))
		return nil
	}
	values, ok := value.([]any)
	if !ok {
		*diagnostics = append(*diagnostics, wrongType(path+"."+key))
		return nil
	}
	ports := make([]Port, 0, len(values))
	for index, item := range values {
		itemPath := path + "." + key + "[" + strconv.Itoa(index) + "]"
		object, ok := item.(map[string]any)
		if !ok {
			*diagnostics = append(*diagnostics, wrongType(itemPath))
			continue
		}
		rejectUnknown(object, itemPath, []string{"protocol", "port"}, diagnostics)
		ports = append(ports, Port{
			Protocol: requiredString(object, "protocol", itemPath, diagnostics),
			Port:     requiredInteger(object, "port", itemPath, diagnostics),
		})
	}
	return ports
}

func validateTyped(resource Resource) []Diagnostic {
	var diagnostics []Diagnostic
	if resource.APIVersion != APIVersion {
		diagnostics = append(diagnostics, unsupportedValue("$.apiVersion"))
	}
	if resource.Kind != Kind {
		diagnostics = append(diagnostics, unsupportedValue("$.kind"))
	}
	if !namePattern.MatchString(resource.Metadata.Name) {
		diagnostics = append(diagnostics, diagnostic("installtarget.identity.invalid_name", "$.metadata.name", "metadata.name must be a lowercase DNS-style name of at most 63 characters"))
	} else if deployment.ReferenceLooksSensitive(resource.Metadata.Name) {
		diagnostics = append(diagnostics, sensitiveReference("$.metadata.name"))
	}
	if !namePattern.MatchString(resource.Spec.DeploymentRef.Name) {
		diagnostics = append(diagnostics, invalidReference("$.spec.deploymentRef.name"))
	} else if deployment.ReferenceLooksSensitive(resource.Spec.DeploymentRef.Name) {
		diagnostics = append(diagnostics, sensitiveReference("$.spec.deploymentRef.name"))
	}
	validateDigest(resource.Spec.DeploymentRef.Digest, "$.spec.deploymentRef.digest", &diagnostics)
	if !validArtifactRef(resource.Spec.Distribution.ArtifactRef) {
		diagnostics = append(diagnostics, diagnostic("installtarget.distribution.artifact_ref", "$.spec.distribution.artifactRef", "Distribution artifact must be a credential-free OCI reference"))
	}
	if len(resource.Spec.Distribution.Version) > 128 || !versionPattern.MatchString(resource.Spec.Distribution.Version) {
		diagnostics = append(diagnostics, unsupportedValue("$.spec.distribution.version"))
	}
	validateDigest(resource.Spec.Distribution.Digest, "$.spec.distribution.digest", &diagnostics)
	if resource.Spec.Profile.Name != ProfilePermissiveDev && resource.Spec.Profile.Name != ProfileHighRisk {
		diagnostics = append(diagnostics, unsupportedValue("$.spec.profile.name"))
	}
	validateDigest(resource.Spec.Profile.Digest, "$.spec.profile.digest", &diagnostics)
	if resource.Spec.Backend.Type != "helm" {
		diagnostics = append(diagnostics, unsupportedValue("$.spec.backend.type"))
	}
	validateHelm(resource.Spec.Backend.Helm, &diagnostics)

	if resource.Spec.Profile.Name == ProfilePermissiveDev && resource.Spec.Bindings != nil {
		diagnostics = append(diagnostics, diagnostic("installtarget.profile.bindings_forbidden", "$.spec.bindings", "permissive-dev must not declare regulated deployment bindings"))
	}
	if resource.Spec.Profile.Name == ProfileHighRisk {
		if resource.Spec.Bindings == nil {
			diagnostics = append(diagnostics, diagnostic("installtarget.profile.bindings_required", "$.spec.bindings", "High-risk profile requires complete deployment bindings"))
		} else {
			validateBindings(*resource.Spec.Bindings, &diagnostics)
		}
	}
	return diagnostics
}

func validateHelm(target HelmTarget, diagnostics *[]Diagnostic) {
	if !referencePattern.MatchString(target.Context) {
		*diagnostics = append(*diagnostics, invalidReference("$.spec.backend.helm.context"))
	} else if deployment.ReferenceLooksSensitive(target.Context) {
		*diagnostics = append(*diagnostics, sensitiveReference("$.spec.backend.helm.context"))
	}
	if !namespacePattern.MatchString(target.Namespace) {
		*diagnostics = append(*diagnostics, unsupportedValue("$.spec.backend.helm.namespace"))
	}
	if !namespacePattern.MatchString(target.ReleaseName) {
		*diagnostics = append(*diagnostics, unsupportedValue("$.spec.backend.helm.releaseName"))
	}
}

func validateBindings(bindings Bindings, diagnostics *[]Diagnostic) {
	if !referencePattern.MatchString(bindings.TenantID) {
		*diagnostics = append(*diagnostics, invalidReference("$.spec.bindings.tenantId"))
	} else if deployment.ReferenceLooksSensitive(bindings.TenantID) {
		*diagnostics = append(*diagnostics, sensitiveReference("$.spec.bindings.tenantId"))
	}
	if !validHTTPSURL(bindings.Exporter.Endpoint) {
		*diagnostics = append(*diagnostics, diagnostic("installtarget.bindings.endpoint", "$.spec.bindings.exporter.endpoint", "Exporter endpoint must be a credential-free HTTPS URL"))
	}
	if len(bindings.Exporter.Egress.CIDRs) == 0 {
		*diagnostics = append(*diagnostics, diagnostic("installtarget.bindings.egress_required", "$.spec.bindings.exporter.egress.cidrs", "At least one restricted canonical egress CIDR is required"))
	}
	if len(bindings.Exporter.Egress.CIDRs) > 64 {
		*diagnostics = append(*diagnostics, diagnostic("installtarget.field.length", "$.spec.bindings.exporter.egress.cidrs", "Egress CIDRs exceed the maximum of 64 entries"))
	}
	seenCIDRs := make(map[string]bool)
	for _, cidr := range bindings.Exporter.Egress.CIDRs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil || prefix.Bits() == 0 || prefix != prefix.Masked() || prefix.String() != cidr || seenCIDRs[cidr] {
			*diagnostics = append(*diagnostics, diagnostic("installtarget.bindings.cidr", "$.spec.bindings.exporter.egress.cidrs", "Egress CIDRs must be unique, canonical, and narrower than a default route"))
			continue
		}
		seenCIDRs[cidr] = true
	}
	if len(bindings.Exporter.Egress.Ports) == 0 {
		*diagnostics = append(*diagnostics, diagnostic("installtarget.bindings.egress_required", "$.spec.bindings.exporter.egress.ports", "At least one restricted TCP egress port is required"))
	}
	if len(bindings.Exporter.Egress.Ports) > 16 {
		*diagnostics = append(*diagnostics, diagnostic("installtarget.field.length", "$.spec.bindings.exporter.egress.ports", "Egress ports exceed the maximum of 16 entries"))
	}
	seenPorts := make(map[int]bool)
	for _, port := range bindings.Exporter.Egress.Ports {
		if port.Protocol != "TCP" || port.Port < 1 || port.Port > 65535 || seenPorts[port.Port] {
			*diagnostics = append(*diagnostics, diagnostic("installtarget.bindings.port", "$.spec.bindings.exporter.egress.ports", "Egress ports must be unique TCP ports from 1 through 65535"))
			continue
		}
		seenPorts[port.Port] = true
	}
	if !referencePattern.MatchString(bindings.UpdateTrust.KeyID) {
		*diagnostics = append(*diagnostics, invalidReference("$.spec.bindings.updateTrust.keyId"))
	} else if deployment.ReferenceLooksSensitive(bindings.UpdateTrust.KeyID) {
		*diagnostics = append(*diagnostics, sensitiveReference("$.spec.bindings.updateTrust.keyId"))
	}
	encodedKey, hasPrefix := strings.CutPrefix(bindings.UpdateTrust.PublicKey, "ed25519:")
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encodedKey)
	if !hasPrefix || err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != encodedKey {
		*diagnostics = append(*diagnostics, diagnostic("installtarget.bindings.public_key", "$.spec.bindings.updateTrust.publicKey", "Update trust publicKey must use ed25519 followed by canonical unpadded base64url for exactly 32 bytes"))
	}
}

func validateDigest(value, path string, diagnostics *[]Diagnostic) {
	if !digestPattern.MatchString(value) {
		*diagnostics = append(*diagnostics, diagnostic("installtarget.digest.invalid", path, "Digest must be lowercase sha256 followed by 64 hexadecimal characters"))
	}
}

func validArtifactRef(value string) bool {
	if len(value) < 9 || len(value) > 512 || !artifactPattern.MatchString(value) || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "oci" || parsed.Opaque != "" || parsed.Host == "" || parsed.Path == "" || parsed.Path == "/" {
		return false
	}
	return parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && validURLHost(parsed) && !pathContainsSensitiveMaterial(parsed.Path)
}

func validHTTPSURL(value string) bool {
	if len(value) < 12 || len(value) > 512 || !endpointPattern.MatchString(value) || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.Host == "" {
		return false
	}
	return parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && validURLHost(parsed) && !pathContainsSensitiveMaterial(parsed.Path)
}

func pathContainsSensitiveMaterial(path string) bool {
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment != "" && deployment.ReferenceLooksSensitive(segment) {
			return true
		}
	}
	return false
}

func validURLHost(parsed *url.URL) bool {
	host := parsed.Hostname()
	if host == "" {
		return false
	}
	if net.ParseIP(host) == nil && (!dnsHostPattern.MatchString(host) || strings.Contains(host, "..")) {
		return false
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
}

// ValidateAgainstDeployment verifies that a target is bound to the exact
// deployment resource and that the shipped profile truthfully matches its
// assurance level. Diagnostics do not echo names or digests.
func ValidateAgainstDeployment(target Resource, referenced deployment.Resource) []Diagnostic {
	if diagnostics := validateTyped(target); len(diagnostics) != 0 {
		return diagnostics
	}
	var diagnostics []Diagnostic
	if referenced.APIVersion != deployment.APIVersion || referenced.Kind != deployment.Kind || referenced.Metadata.Name == "" {
		return []Diagnostic{diagnostic("installtarget.compatibility.deployment_invalid", "$.spec.deploymentRef", "Referenced deployment is not a valid FabricDeployment identity")}
	}
	if target.Spec.DeploymentRef.Name != referenced.Metadata.Name {
		diagnostics = append(diagnostics, diagnostic("installtarget.compatibility.name", "$.spec.deploymentRef.name", "Install target does not reference the supplied deployment name"))
	}
	// Reconstruct the generic contract document before hashing. encoding/json
	// preserves struct field declaration order, while deployment review identity
	// is defined over lexicographically keyed generic objects.
	digest, err := deployment.Digest(deploymentDocument(referenced))
	if err != nil || target.Spec.DeploymentRef.Digest != digest {
		diagnostics = append(diagnostics, diagnostic("installtarget.compatibility.digest", "$.spec.deploymentRef.digest", "Install target does not reference the supplied deployment digest"))
	}
	switch target.Spec.Profile.Name {
	case ProfilePermissiveDev:
		if referenced.Spec.AssuranceLevel != "A0" {
			diagnostics = append(diagnostics, diagnostic("installtarget.compatibility.assurance", "$.spec.profile.name", "permissive-dev is compatible only with A0 deployments"))
		}
	case ProfileHighRisk:
		if referenced.Spec.AssuranceLevel != "A2" && referenced.Spec.AssuranceLevel != "A3" {
			diagnostics = append(diagnostics, diagnostic("installtarget.compatibility.assurance", "$.spec.profile.name", "eu-ai-act-high-risk is compatible only with A2 or A3 deployments"))
		}
	}
	return diagnostics
}

func deploymentDocument(resource deployment.Resource) map[string]any {
	connection := map[string]any{
		"mode":         resource.Spec.Connection.Mode,
		"tenantIdFrom": resource.Spec.Connection.TenantIDFrom,
	}
	if resource.Spec.Connection.WorkloadIdentityRef != "" {
		connection["workloadIdentityRef"] = resource.Spec.Connection.WorkloadIdentityRef
	}
	observe := map[string]any{"contentMode": resource.Spec.Observe.ContentMode}
	if resource.Spec.Observe.RelayRef != "" {
		observe["relayRef"] = resource.Spec.Observe.RelayRef
	}
	spec := map[string]any{
		"assuranceLevel": resource.Spec.AssuranceLevel,
		"connection":     connection,
		"observe":        observe,
	}
	if controls := resource.Spec.Controls; controls != nil {
		value := map[string]any{"profileRef": controls.ProfileRef}
		if controls.PolicyRef != "" {
			value["policyRef"] = controls.PolicyRef
		}
		if controls.AuthorizationRef != "" {
			value["authorizationRef"] = controls.AuthorizationRef
		}
		if controls.PIIRef != "" {
			value["piiRef"] = controls.PIIRef
		}
		if controls.GuardrailRef != "" {
			value["guardrailRef"] = controls.GuardrailRef
		}
		if controls.EscalationRef != "" {
			value["escalationRef"] = controls.EscalationRef
		}
		spec["controls"] = value
	}
	if resource.Spec.Assurance != nil {
		spec["assurance"] = map[string]any{"planRef": resource.Spec.Assurance.PlanRef}
	}
	if resource.Spec.Rollout != nil {
		spec["rollout"] = map[string]any{"approvalRef": resource.Spec.Rollout.ApprovalRef}
	}
	return map[string]any{
		"apiVersion": resource.APIVersion,
		"kind":       resource.Kind,
		"metadata":   map[string]any{"name": resource.Metadata.Name},
		"spec":       spec,
	}
}

func findForbiddenKeys(value any) []Diagnostic {
	var diagnostics []Diagnostic
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range sortedKeys(typed) {
			if forbiddenKeys[normalizedKey(key)] {
				diagnostics = append(diagnostics, diagnostic("installtarget.security.inline_sensitive_value", "$", "Inline secrets, credentials, tokens, and environment dumps are forbidden"))
			}
			diagnostics = append(diagnostics, findForbiddenKeys(typed[key])...)
		}
	case []any:
		for _, child := range typed {
			diagnostics = append(diagnostics, findForbiddenKeys(child)...)
		}
	}
	return diagnostics
}

func normalizedKey(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(value) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func rejectUnknown(object map[string]any, path string, allowed []string, diagnostics *[]Diagnostic) {
	allow := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		allow[key] = true
	}
	for _, key := range sortedKeys(object) {
		if !allow[key] {
			*diagnostics = append(*diagnostics, diagnostic("installtarget.field.unknown", path+"[*]", "Unknown install target field is forbidden"))
		}
	}
}

func requiredString(object map[string]any, key, path string, diagnostics *[]Diagnostic) string {
	value, exists := object[key]
	if !exists {
		*diagnostics = append(*diagnostics, requiredField(path+"."+key))
		return ""
	}
	text, ok := value.(string)
	if !ok {
		*diagnostics = append(*diagnostics, wrongType(path+"."+key))
		return ""
	}
	return text
}

func requiredBool(object map[string]any, key, path string, diagnostics *[]Diagnostic) bool {
	value, exists := object[key]
	if !exists {
		*diagnostics = append(*diagnostics, requiredField(path+"."+key))
		return false
	}
	result, ok := value.(bool)
	if !ok {
		*diagnostics = append(*diagnostics, wrongType(path+"."+key))
		return false
	}
	return result
}

func requiredInteger(object map[string]any, key, path string, diagnostics *[]Diagnostic) int {
	value, exists := object[key]
	if !exists {
		*diagnostics = append(*diagnostics, requiredField(path+"."+key))
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		parsed, err := strconv.ParseInt(string(typed), 10, 32)
		if err == nil {
			return int(parsed)
		}
	}
	*diagnostics = append(*diagnostics, wrongType(path+"."+key))
	return 0
}

func requiredStringArray(object map[string]any, key, path string, diagnostics *[]Diagnostic) []string {
	value, exists := object[key]
	if !exists {
		*diagnostics = append(*diagnostics, requiredField(path+"."+key))
		return nil
	}
	values, ok := value.([]any)
	if !ok {
		*diagnostics = append(*diagnostics, wrongType(path+"."+key))
		return nil
	}
	result := make([]string, 0, len(values))
	for index, value := range values {
		text, ok := value.(string)
		if !ok {
			*diagnostics = append(*diagnostics, wrongType(path+"."+key+"["+strconv.Itoa(index)+"]"))
			continue
		}
		result = append(result, text)
	}
	return result
}

func requiredObject(object map[string]any, key, path string, diagnostics *[]Diagnostic) map[string]any {
	value, exists := object[key]
	if !exists {
		*diagnostics = append(*diagnostics, requiredField(path+"."+key))
		return nil
	}
	typed, ok := value.(map[string]any)
	if !ok {
		*diagnostics = append(*diagnostics, wrongType(path+"."+key))
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
		*diagnostics = append(*diagnostics, wrongType(path+"."+key))
		return nil, true
	}
	return typed, true
}

func requiredField(path string) Diagnostic {
	return diagnostic("installtarget.field.required", path, "Required install target field is missing")
}

func wrongType(path string) Diagnostic {
	return diagnostic("installtarget.field.type", path, "Install target field has the wrong type")
}

func unsupportedValue(path string) Diagnostic {
	return diagnostic("installtarget.field.value", path, "Install target field has an unsupported value")
}

func invalidReference(path string) Diagnostic {
	return diagnostic("installtarget.reference.invalid", path, "Reference must be a bounded identifier and must not contain inline material")
}

func sensitiveReference(path string) Diagnostic {
	return diagnostic("installtarget.reference.sensitive", path, "Reference resembles credential material; use an external reference identifier instead")
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
