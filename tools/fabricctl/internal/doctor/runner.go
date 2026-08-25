// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const noSecretsNotice = "Kubernetes GET output is restricted to the object name; Secret values are never printed."

func Run(ctx context.Context, opts Options, deps Dependencies) Report {
	p, _ := profileByName(opts.Profile)
	requirements := requirementsFor(p, opts.Requirements)
	results := []Result{checkPlatform(deps.Platform)}

	kubectlAvailable, kubectlResults := checkTool(ctx, opts, deps.Commands, "kubectl", false)
	results = append(results, kubectlResults...)
	helmRequired := p.Name == "eu-ai-act-high-risk"
	helmAvailable, helmResults := checkTool(ctx, opts, deps.Commands, "helm", helmRequired)
	results = append(results, helmResults...)
	results = append(results, checkHelmRender(ctx, opts, deps.Commands, p, helmAvailable))

	if kubectlAvailable {
		results = append(results, checkKubeContext(ctx, opts, deps.Commands))
		results = append(results, checkKubeReachability(ctx, opts, deps.Commands))
		results = append(results, checkNamespaceAccess(ctx, opts, deps.Commands))
	} else {
		required := len(requirements) > 0
		results = append(results,
			unavailableKubeResult("KUBE-CONTEXT-001", "Kubernetes context was not inspected", required),
			unavailableKubeResult("KUBE-API-001", "Kubernetes API reachability was not inspected", required),
			unavailableKubeResult("KUBE-NAMESPACE-AUTH-001", "Namespace authorization was not inspected", required),
		)
	}

	results = append(results, checkProfileRequirements(p, len(requirements)))
	results = append(results, checkEndpoint(ctx, opts, p, deps.HTTP)...)
	for _, requirement := range requirements {
		if kubectlAvailable {
			results = append(results, checkObject(ctx, opts, deps.Commands, requirement))
		} else {
			results = append(results, result(requirement.Code, SeverityError, StatusFail, true,
				fmt.Sprintf("Required %s %q could not be verified", requirement.Kind, requirement.Name),
				"Install kubectl, select the intended cluster, and rerun doctor.",
				noSecretsNotice))
		}
	}
	results = append(results, result("NETWORKPOLICY-ENFORCEMENT-001", SeverityWarning, StatusWarn, false,
		"Kubernetes NetworkPolicy enforcement cannot be proven generically",
		"Verify that the cluster CNI enforces NetworkPolicy and run an environment-specific deny/allow connectivity test before production.",
		"Manifest presence does not prove dataplane enforcement."))

	return newReport(opts, results)
}

func checkPlatform(p platform) Result {
	osName, arch := p.OS(), p.Arch()
	if (osName == "linux" || osName == "darwin") && (arch == "amd64" || arch == "arm64") {
		return result("PLATFORM-001", SeverityInfo, StatusPass, true, "Host platform is supported", "No action required.", "os="+osName, "arch="+arch)
	}
	return result("PLATFORM-001", SeverityError, StatusFail, true, "Host platform is not supported", "Use Linux or macOS on amd64 or arm64.", "os="+osName, "arch="+arch)
}

func checkTool(ctx context.Context, opts Options, commands CommandRunner, tool string, required bool) (bool, []Result) {
	code := strings.ToUpper(tool) + "-PRESENT-001"
	path, err := commands.LookPath(tool)
	if err != nil {
		severity, status := SeverityWarning, StatusWarn
		if required {
			severity, status = SeverityError, StatusFail
		}
		return false, []Result{result(code, severity, status, required,
			tool+" is not installed or is not on PATH",
			"Install "+tool+" to enable Kubernetes deployment checks.")}
	}

	args := []string{"version", "--short"}
	if tool == "kubectl" {
		args = []string{"version", "--client", "--output=json"}
	}
	commandCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	out, runErr := commands.Run(commandCtx, tool, args...)
	version := sanitizeVersion(tool, out.Stdout)
	if runErr != nil {
		return true, []Result{result(code, SeverityWarning, StatusWarn, false,
			tool+" was found but its version could not be determined",
			"Confirm the "+tool+" binary is executable and supported.", "path="+path)}
	}
	return true, []Result{result(code, SeverityInfo, StatusPass, required,
		tool+" is available", "No action required.", "path="+path, "version="+version)}
}

func sanitizeVersion(tool, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if tool == "kubectl" {
		var payload struct {
			ClientVersion struct {
				GitVersion string `json:"gitVersion"`
			} `json:"clientVersion"`
		}
		if json.Unmarshal([]byte(trimmed), &payload) == nil && payload.ClientVersion.GitVersion != "" {
			return payload.ClientVersion.GitVersion
		}
	}
	if index := strings.IndexByte(trimmed, '\n'); index >= 0 {
		trimmed = trimmed[:index]
	}
	if len(trimmed) > 120 {
		trimmed = trimmed[:120]
	}
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}

func unavailableKubeResult(code, summary string, required bool) Result {
	if required {
		return result(code, SeverityError, StatusFail, true, summary, "Install kubectl and select the intended cluster, then rerun doctor.")
	}
	return result(code, SeverityWarning, StatusSkip, false, summary, "Install kubectl to enable this optional check.")
}

func checkKubeContext(ctx context.Context, opts Options, commands CommandRunner) Result {
	out, err := runCommand(ctx, opts, commands, "kubectl", "config", "current-context")
	contextName := strings.TrimSpace(out.Stdout)
	if err != nil || contextName == "" {
		return result("KUBE-CONTEXT-001", SeverityWarning, StatusWarn, false, "No active Kubernetes context was found", "Select the intended context with kubectl config use-context.")
	}
	return result("KUBE-CONTEXT-001", SeverityInfo, StatusPass, false, "Kubernetes context is selected", "Confirm this is the intended production boundary before installation.", "context="+contextName)
}

func checkKubeReachability(ctx context.Context, opts Options, commands CommandRunner) Result {
	out, err := runCommand(ctx, opts, commands, "kubectl", "get", "--raw=/readyz")
	if err != nil || strings.TrimSpace(out.Stdout) != "ok" {
		return result("KUBE-API-001", SeverityWarning, StatusWarn, false, "Kubernetes API readiness could not be confirmed", "Check cluster credentials, network access, and API server health.")
	}
	return result("KUBE-API-001", SeverityInfo, StatusPass, false, "Kubernetes API is reachable and ready", "No action required.", "readyz=ok")
}

func checkNamespaceAccess(ctx context.Context, opts Options, commands CommandRunner) Result {
	secret, secretErr := runCommand(ctx, opts, commands, "kubectl", "auth", "can-i", "get", "secrets", "--namespace", opts.Namespace)
	configMap, configMapErr := runCommand(ctx, opts, commands, "kubectl", "auth", "can-i", "get", "configmaps", "--namespace", opts.Namespace)
	secretAllowed := secretErr == nil && strings.TrimSpace(secret.Stdout) == "yes"
	configMapAllowed := configMapErr == nil && strings.TrimSpace(configMap.Stdout) == "yes"
	if !secretAllowed || !configMapAllowed {
		return result("KUBE-NAMESPACE-AUTH-001", SeverityWarning, StatusWarn, false,
			"Read access to required resource types is incomplete in the target namespace",
			"Grant the deployment identity least-privilege get access to the required named Secrets and ConfigMaps, then rerun doctor.",
			fmt.Sprintf("namespace=%s", opts.Namespace), fmt.Sprintf("can_get_secrets=%t", secretAllowed), fmt.Sprintf("can_get_configmaps=%t", configMapAllowed), noSecretsNotice)
	}
	return result("KUBE-NAMESPACE-AUTH-001", SeverityInfo, StatusPass, false,
		"Target namespace resource access is available", "Keep access scoped to required names where your authorization system permits it.",
		"namespace="+opts.Namespace, "can_get_secrets=true", "can_get_configmaps=true", noSecretsNotice)
}

func checkProfileRequirements(p profile, requirementCount int) Result {
	if requirementCount == 0 && !p.EndpointRequired {
		return result("PROFILE-REQUIREMENTS-001", SeverityInfo, StatusPass, true,
			fmt.Sprintf("Profile %q has no mandatory external endpoint or Kubernetes objects", p.Name),
			"Use eu-ai-act-high-risk when its stricter controls are required.", "profile="+p.Name)
	}
	return result("PROFILE-REQUIREMENTS-001", SeverityInfo, StatusPass, true,
		fmt.Sprintf("Profile %q requirements are being evaluated", p.Name),
		"Resolve every failed required check before installation.", "profile="+p.Name, fmt.Sprintf("required_objects=%d", requirementCount), fmt.Sprintf("endpoint_required=%t", p.EndpointRequired))
}

func checkHelmRender(ctx context.Context, opts Options, commands CommandRunner, p profile, helmAvailable bool) Result {
	required := p.Name == "eu-ai-act-high-risk"
	if !helmAvailable {
		if required {
			return result("HELM-RENDER-001", SeverityError, StatusFail, true,
				"Required Helm chart validation could not run",
				"Install Helm, then rerun doctor with --chart and ordered --values files.")
		}
		return result("HELM-RENDER-001", SeverityInfo, StatusSkip, false,
			"Helm chart validation was not available", "Install Helm and pass --chart to enable this advisory check.")
	}
	if opts.Chart == "" {
		if required {
			return result("HELM-RENDER-001", SeverityError, StatusFail, true,
				"High-risk preflight requires the exact local Fabric chart",
				"Pass --chart and every ordered deployment overlay with repeatable --values flags.")
		}
		return result("HELM-RENDER-001", SeverityInfo, StatusSkip, false,
			"No Helm chart was supplied", "Pass --chart to opt in to a read-only render and built-in chart validation.")
	}
	if required && len(opts.Values) == 0 {
		return result("HELM-RENDER-001", SeverityError, StatusFail, true,
			"High-risk preflight requires explicit values overlays",
			"Pass the high-risk profile and all customer overlays in installation order with repeatable --values flags; inline --set data is not accepted.")
	}
	if err := validateLocalChartInput(opts.Chart, opts.Values); err != nil {
		return helmRenderFailure(required, "Helm chart inputs are not safe local files", "Use an existing local chart and existing local values files; URLs and standard input are not accepted.", "input_validation="+err.Error())
	}

	args := []string{"template", "fabric-doctor", opts.Chart, "--namespace", opts.Namespace, "--skip-tests"}
	if required {
		args = append(args, "--validate")
	}
	for _, valuesFile := range opts.Values {
		args = append(args, "--values", valuesFile)
	}
	out, err := runCommand(ctx, opts, commands, "helm", args...)
	if err != nil {
		return helmRenderFailure(required,
			"Helm could not render and validate the selected deployment",
			"Run Helm template locally with the same files, correct every chart validation error (including tenant identity, destination and trusted update key), and rerun doctor. Helm output is intentionally suppressed by doctor.")
	}
	if required {
		missing := missingHighRiskRenderProofs(out.Stdout)
		if len(missing) > 0 {
			return helmRenderFailure(true,
				"Rendered chart does not prove the effective eu-ai-act-high-risk invariants",
				"Use the shipped Fabric chart and high-risk profile, preserve its chart-owned invariant table, and correct overlays that remove required Collector or update-agent controls.",
				fmt.Sprintf("missing_invariant_proofs=%d", len(missing)), "helm_output=suppressed")
		}
	}
	return result("HELM-RENDER-001", SeverityInfo, StatusPass, required,
		"Helm chart render and built-in validation succeeded",
		"Retain the exact ordered values files with deployment evidence; rendered manifests may contain sensitive material and are not included here.",
		fmt.Sprintf("values_files=%d", len(opts.Values)), fmt.Sprintf("cluster_openapi_validation=%t", required), "helm_output=suppressed")
}

type renderProof struct {
	token string
	count int
}

func missingHighRiskRenderProofs(rendered string) []string {
	proofs := map[string]renderProof{
		"contract-marker":             {token: `singleaxis.com/assurance-contract: "eu-ai-act-high-risk-v1"`, count: 1},
		"profile-identity":            {token: `singleaxis.com/profile: "eu-ai-act-high-risk"`, count: 1},
		"collector-config":            {token: "# Source: fabric/charts/otel-collector/templates/configmap.yaml", count: 1},
		"guard-definition":            {token: "      fabricguard:\n", count: 1},
		"drop-unknown":                {token: "        drop_unknown_classes: true\n", count: 1},
		"guard-in-both-pipelines":     {token: "            - fabricguard\n", count: 2},
		"policy-definition":           {token: "      fabricpolicy:\n", count: 1},
		"policy-in-both-pipelines":    {token: "            - fabricpolicy\n", count: 2},
		"redaction-definition":        {token: "      fabricredact:\n", count: 1},
		"redaction-in-both-pipelines": {token: "            - fabricredact\n", count: 2},
		"traces-pipeline":             {token: "        traces:\n", count: 1},
		"secure-exporter":             {token: "          insecure: false\n", count: 1},
		"update-agent-config":         {token: "# Source: fabric/charts/update-agent/templates/configmap.yaml", count: 1},
		"update-agent-fail-closed":    {token: "    fail_closed: true\n", count: 1},
		"update-agent-deployment":     {token: "# Source: fabric/charts/update-agent/templates/deployment.yaml", count: 1},
		"webhook":                     {token: "# Source: fabric/charts/update-agent/templates/validatingwebhookconfiguration.yaml", count: 1},
		"webhook-failure-policy":      {token: "    failurePolicy: Fail\n", count: 1},
	}
	missing := make([]string, 0)
	for name, proof := range proofs {
		if strings.Count(rendered, proof.token) < proof.count {
			missing = append(missing, name)
		}
	}
	return missing
}

func helmRenderFailure(required bool, summary, remediation string, evidence ...string) Result {
	if required {
		return result("HELM-RENDER-001", SeverityError, StatusFail, true, summary, remediation, evidence...)
	}
	return result("HELM-RENDER-001", SeverityWarning, StatusWarn, false, summary, remediation, evidence...)
}

func validateLocalChartInput(chart string, values []string) error {
	if isRemoteOrStdin(chart) {
		return errors.New("chart must be a local path")
	}
	chartInfo, err := os.Stat(chart)
	if err != nil {
		return errors.New("chart path does not exist")
	}
	if chartInfo.IsDir() {
		if _, err := os.Stat(filepath.Join(chart, "Chart.yaml")); err != nil {
			return errors.New("chart directory has no Chart.yaml")
		}
	} else if !chartInfo.Mode().IsRegular() {
		return errors.New("chart path is not a directory or regular archive")
	}
	for _, valuesFile := range values {
		if isRemoteOrStdin(valuesFile) {
			return errors.New("values input must be a local file")
		}
		info, err := os.Stat(valuesFile)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("values file does not exist or is not regular")
		}
	}
	return nil
}

func isRemoteOrStdin(value string) bool {
	return value == "-" || strings.Contains(value, "://")
}

func checkObject(ctx context.Context, opts Options, commands CommandRunner, requirement objectRequirement) Result {
	out, err := runCommand(ctx, opts, commands, "kubectl", "get", requirement.Kind, requirement.Name, "--namespace", opts.Namespace, "--ignore-not-found", "--output=name")
	found := err == nil && strings.TrimSpace(out.Stdout) != ""
	if !found {
		return result(requirement.Code, SeverityError, StatusFail, true,
			fmt.Sprintf("Required %s %q is absent or unreadable", requirement.Kind, requirement.Name),
			fmt.Sprintf("Provision the %s for the %s in namespace %q and grant least-privilege read access.", requirement.Kind, requirement.Why, opts.Namespace),
			"namespace="+opts.Namespace, noSecretsNotice)
	}
	return result(requirement.Code, SeverityInfo, StatusPass, true,
		fmt.Sprintf("Required %s %q is present", requirement.Kind, requirement.Name),
		"No action required.", "namespace="+opts.Namespace, noSecretsNotice)
}

func checkEndpoint(ctx context.Context, opts Options, p profile, client HTTPClient) []Result {
	if opts.Endpoint == "" {
		if p.EndpointRequired {
			return []Result{
				result("DESTINATION-URL-001", SeverityError, StatusFail, true, "Profile requires a telemetry destination URL", "Pass an approved HTTPS OTLP destination with --endpoint."),
				result("DESTINATION-REACHABILITY-001", SeverityError, StatusFail, true, "Required destination reachability was not tested", "Set --endpoint and rerun doctor."),
			}
		}
		return []Result{
			result("DESTINATION-URL-001", SeverityInfo, StatusSkip, false, "No destination URL was supplied", "Pass --endpoint to opt in to URL and reachability checks."),
			result("DESTINATION-REACHABILITY-001", SeverityInfo, StatusSkip, false, "Destination reachability check was not requested", "Pass --endpoint to opt in to a read-only HEAD probe."),
		}
	}

	parsed, err := validateEndpoint(opts.Endpoint, p.HTTPSRequired)
	if err != nil {
		return []Result{
			result("DESTINATION-URL-001", SeverityError, StatusFail, true, "Destination URL is invalid", "Provide an http(s) URL without credentials, query parameters, or fragments; high-risk profiles require HTTPS.", "validation_error="+err.Error()),
			result("DESTINATION-REACHABILITY-001", SeverityError, StatusFail, true, "Destination was not probed because URL validation failed", "Correct --endpoint and rerun doctor."),
		}
	}
	evidence := []string{"scheme=" + parsed.Scheme, "host=" + parsed.Hostname()}
	validation := result("DESTINATION-URL-001", SeverityInfo, StatusPass, true, "Destination URL is structurally valid", "Confirm the destination is approved by your organization.", evidence...)

	request, _ := http.NewRequestWithContext(ctx, http.MethodHead, parsed.String(), nil)
	request.Header.Set("User-Agent", "fabricctl-doctor/"+opts.Version)
	probeCtx, cancel := context.WithTimeout(request.Context(), opts.Timeout)
	defer cancel()
	request = request.WithContext(probeCtx)
	response, probeErr := client.Do(request)
	if probeErr != nil {
		return []Result{validation, result("DESTINATION-REACHABILITY-001", SeverityError, StatusFail, true,
			"Destination could not be reached", "Verify DNS, TLS trust, firewall and NetworkPolicy egress for the approved destination.", evidence...)}
	}
	defer response.Body.Close()
	reachability := result("DESTINATION-REACHABILITY-001", SeverityInfo, StatusPass, true,
		"Destination accepted a network connection", "Validate authenticated OTLP ingestion separately before production.", append(evidence, fmt.Sprintf("http_status=%d", response.StatusCode))...)
	return []Result{validation, reachability}
}

func validateEndpoint(raw string, httpsRequired bool) (*url.URL, error) {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return nil, errors.New("malformed URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("scheme must be http or https")
	}
	if httpsRequired && parsed.Scheme != "https" {
		return nil, errors.New("selected profile requires https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, errors.New("host is required")
	}
	if parsed.User != nil {
		return nil, errors.New("embedded credentials are forbidden")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("query parameters and fragments are forbidden")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && ip.IsUnspecified() {
		return nil, errors.New("unspecified destination address is forbidden")
	}
	return parsed, nil
}

func runCommand(ctx context.Context, opts Options, commands CommandRunner, name string, args ...string) (CommandOutput, error) {
	commandCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	output, err := commands.Run(commandCtx, name, args...)
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		return output, context.DeadlineExceeded
	}
	if errors.Is(err, exec.ErrNotFound) {
		return output, exec.ErrNotFound
	}
	return output, err
}
