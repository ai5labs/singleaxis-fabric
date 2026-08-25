package doctor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakePlatform struct{ os, arch string }

func (p fakePlatform) OS() string   { return p.os }
func (p fakePlatform) Arch() string { return p.arch }

type commandResponse struct {
	out CommandOutput
	err error
}

type fakeCommands struct {
	paths     map[string]string
	responses map[string]commandResponse
	calls     []string
}

func (f *fakeCommands) LookPath(file string) (string, error) {
	if path, ok := f.paths[file]; ok {
		return path, nil
	}
	return "", errors.New("not found")
}

func (f *fakeCommands) Run(_ context.Context, name string, args ...string) (CommandOutput, error) {
	key := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, key)
	response, ok := f.responses[key]
	if !ok {
		return CommandOutput{}, errors.New("unexpected command: " + key)
	}
	return response.out, response.err
}

type failingHTTP struct{ called bool }

func (f *failingHTTP) Do(_ *http.Request) (*http.Response, error) {
	f.called = true
	return nil, errors.New("unexpected HTTP call")
}

type recordingHTTP struct {
	request *http.Request
	status  int
}

func (r *recordingHTTP) Do(request *http.Request) (*http.Response, error) {
	r.request = request
	return &http.Response{
		StatusCode: r.status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func defaultOptions() Options {
	return Options{Profile: "unprofiled", Namespace: "fabric-system", Timeout: time.Second, Version: "test"}
}

func localChartInputs(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	chart := filepath.Join(root, "fabric")
	if err := os.Mkdir(chart, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chart, "Chart.yaml"), []byte("apiVersion: v2\nname: fabric\nversion: 0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := filepath.Join(root, "high-risk.yaml")
	if err := os.WriteFile(values, []byte("profile:\n  name: eu-ai-act-high-risk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return chart, values
}

func highRiskCommands(opts Options, helmResponse commandResponse) *fakeCommands {
	responses := map[string]commandResponse{
		"kubectl version --client --output=json":                      {out: CommandOutput{Stdout: `{"clientVersion":{"gitVersion":"v1.31.0"}}`}},
		"helm version --short":                                        {out: CommandOutput{Stdout: "v3.16.0\n"}},
		"kubectl config current-context":                              {out: CommandOutput{Stdout: "regulated-prod\n"}},
		"kubectl get --raw=/readyz":                                   {out: CommandOutput{Stdout: "ok"}},
		"kubectl auth can-i get secrets --namespace fabric-system":    {out: CommandOutput{Stdout: "yes\n"}},
		"kubectl auth can-i get configmaps --namespace fabric-system": {out: CommandOutput{Stdout: "yes\n"}},
	}
	helmArgs := []string{"template", "fabric-doctor", opts.Chart, "--namespace", opts.Namespace, "--skip-tests"}
	if opts.Profile == "eu-ai-act-high-risk" {
		helmArgs = append(helmArgs, "--validate")
	}
	for _, valuesFile := range opts.Values {
		helmArgs = append(helmArgs, "--values", valuesFile)
	}
	responses["helm "+strings.Join(helmArgs, " ")] = helmResponse
	p, _ := profileByName(opts.Profile)
	for _, requirement := range requirementsFor(p, opts.Requirements) {
		call := "kubectl get " + requirement.Kind + " " + requirement.Name + " --namespace fabric-system --ignore-not-found --output=name"
		responses[call] = commandResponse{out: CommandOutput{Stdout: requirement.Kind + "/" + requirement.Name + "\n"}}
	}
	return &fakeCommands{
		paths:     map[string]string{"kubectl": "/bin/kubectl", "helm": "/bin/helm"},
		responses: responses,
	}
}

func highRiskRenderedProof() string {
	return `
singleaxis.com/profile: "eu-ai-act-high-risk"
singleaxis.com/assurance-contract: "eu-ai-act-high-risk-v1"
# Source: fabric/charts/otel-collector/templates/configmap.yaml
      fabricguard:
        drop_unknown_classes: true
      fabricpolicy:
      fabricredact:
          insecure: false
        logs:
            - fabricguard
            - fabricredact
            - fabricpolicy
        traces:
            - fabricguard
            - fabricredact
            - fabricpolicy
# Source: fabric/charts/update-agent/templates/configmap.yaml
    fail_closed: true
# Source: fabric/charts/update-agent/templates/deployment.yaml
# Source: fabric/charts/update-agent/templates/validatingwebhookconfiguration.yaml
    failurePolicy: Fail
`
}

func noToolDependencies() (Dependencies, *failingHTTP) {
	httpClient := &failingHTTP{}
	return Dependencies{
		Commands: &fakeCommands{paths: map[string]string{}, responses: map[string]commandResponse{}},
		Platform: fakePlatform{os: "linux", arch: "amd64"},
		HTTP:     httpClient,
	}, httpClient
}

func resultByCode(t *testing.T, report Report, code string) Result {
	t.Helper()
	for _, result := range report.Results {
		if result.Code == code {
			return result
		}
	}
	t.Fatalf("result %s not found", code)
	return Result{}
}

func TestUnprofiledRemainsUsefulWithoutKubernetes(t *testing.T) {
	deps, httpClient := noToolDependencies()
	report := Run(context.Background(), defaultOptions(), deps)

	if report.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q", report.SchemaVersion)
	}
	if report.Summary.FailedRequired != 0 {
		t.Fatalf("required failures = %d, want 0", report.Summary.FailedRequired)
	}
	if resultByCode(t, report, "KUBECTL-PRESENT-001").Status != StatusWarn {
		t.Fatal("missing kubectl should be a warning")
	}
	if resultByCode(t, report, "KUBE-API-001").Status != StatusSkip {
		t.Fatal("API check should skip without optional kubectl")
	}
	if httpClient.called {
		t.Fatal("HTTP should not be called without --endpoint")
	}
}

func TestHighRiskFailsClosedWithoutRequirements(t *testing.T) {
	deps, _ := noToolDependencies()
	opts := defaultOptions()
	opts.Profile = "eu-ai-act-high-risk"
	report := Run(context.Background(), opts, deps)

	if report.Summary.FailedRequired == 0 {
		t.Fatal("high-risk profile should fail when requirements cannot be verified")
	}
	for _, code := range []string{
		"HELM-PRESENT-001",
		"HELM-RENDER-001",
		"DESTINATION-URL-001",
		"DESTINATION-REACHABILITY-001",
		"PROFILE-SECRET-PRESIDIO-001",
		"PROFILE-SECRET-SAMPLER-001",
		"PROFILE-CONFIGMAP-POLICY-001",
		"PROFILE-CONFIGMAP-RAILS-001",
	} {
		got := resultByCode(t, report, code)
		if got.Status != StatusFail || !got.Required {
			t.Fatalf("%s = status %s required %t", code, got.Status, got.Required)
		}
	}
}

func TestHighRiskHappyPathUsesOnlyMetadataChecks(t *testing.T) {
	opts := defaultOptions()
	opts.Profile = "eu-ai-act-high-risk"
	opts.Endpoint = "https://collector.example.com/otlp"
	chart, values := localChartInputs(t)
	opts.Chart = chart
	opts.Values = []string{values}
	commands := highRiskCommands(opts, commandResponse{out: CommandOutput{Stdout: highRiskRenderedProof()}})
	httpClient := &recordingHTTP{status: http.StatusUnauthorized}
	report := Run(context.Background(), opts, Dependencies{
		Commands: commands,
		Platform: fakePlatform{os: "linux", arch: "arm64"},
		HTTP:     httpClient,
	})

	if report.Summary.FailedRequired != 0 {
		t.Fatalf("required failures = %d", report.Summary.FailedRequired)
	}
	if got := resultByCode(t, report, "DESTINATION-REACHABILITY-001"); got.Status != StatusPass {
		t.Fatalf("reachability status = %s", got.Status)
	}
	if httpClient.request == nil || httpClient.request.Method != http.MethodHead {
		t.Fatalf("request = %#v, want HEAD", httpClient.request)
	}
	for _, call := range commands.calls {
		if strings.Contains(call, "-o json") || strings.Contains(call, "-o yaml") {
			t.Fatalf("potential object content read: %s", call)
		}
	}
}

func TestHighRiskCannotPassWhenChartValidationFails(t *testing.T) {
	opts := defaultOptions()
	opts.Profile = "eu-ai-act-high-risk"
	opts.Endpoint = "https://collector.example.com/otlp"
	chart, values := localChartInputs(t)
	opts.Chart = chart
	opts.Values = []string{values}
	commands := highRiskCommands(opts, commandResponse{
		out: CommandOutput{Stdout: "rendered secret: do-not-report"},
		err: errors.New("trusted update key is missing: do-not-report"),
	})
	report := Run(context.Background(), opts, Dependencies{
		Commands: commands,
		Platform: fakePlatform{os: "linux", arch: "arm64"},
		HTTP:     &recordingHTTP{status: http.StatusOK},
	})

	got := resultByCode(t, report, "HELM-RENDER-001")
	if got.Status != StatusFail || !got.Required {
		t.Fatalf("render = %s required=%t", got.Status, got.Required)
	}
	var output bytes.Buffer
	if err := Render(&output, report, "json"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "do-not-report") {
		t.Fatal("Helm output or error leaked into report")
	}
}

func TestHighRiskCannotPassOnProfileLabelAlone(t *testing.T) {
	opts := defaultOptions()
	opts.Profile = "eu-ai-act-high-risk"
	opts.Endpoint = "https://collector.example.com/otlp"
	chart, values := localChartInputs(t)
	opts.Chart = chart
	opts.Values = []string{values}
	commands := highRiskCommands(opts, commandResponse{out: CommandOutput{Stdout: `singleaxis.com/profile: "eu-ai-act-high-risk"`}})
	report := Run(context.Background(), opts, Dependencies{
		Commands: commands,
		Platform: fakePlatform{os: "linux", arch: "amd64"},
		HTTP:     &recordingHTTP{status: http.StatusOK},
	})

	got := resultByCode(t, report, "HELM-RENDER-001")
	if got.Status != StatusFail || !got.Required || !strings.Contains(got.Summary, "effective") {
		t.Fatalf("render = %s required=%t summary=%q", got.Status, got.Required, got.Summary)
	}
}

func TestHighRiskCannotPassWithFailOpenRenderedWebhook(t *testing.T) {
	opts := defaultOptions()
	opts.Profile = "eu-ai-act-high-risk"
	opts.Endpoint = "https://collector.example.com/otlp"
	chart, values := localChartInputs(t)
	opts.Chart = chart
	opts.Values = []string{values}
	rendered := strings.Replace(highRiskRenderedProof(), "failurePolicy: Fail", "failurePolicy: Ignore", 1)
	commands := highRiskCommands(opts, commandResponse{out: CommandOutput{Stdout: rendered}})
	report := Run(context.Background(), opts, Dependencies{
		Commands: commands,
		Platform: fakePlatform{os: "linux", arch: "amd64"},
		HTTP:     &recordingHTTP{status: http.StatusOK},
	})

	if got := resultByCode(t, report, "HELM-RENDER-001"); got.Status != StatusFail || !got.Required {
		t.Fatalf("render = %s required=%t", got.Status, got.Required)
	}
}

func TestHighRiskNamedRequirementOverrides(t *testing.T) {
	opts := defaultOptions()
	opts.Profile = "eu-ai-act-high-risk"
	opts.Endpoint = "https://collector.example.com/otlp"
	chart, values := localChartInputs(t)
	opts.Chart = chart
	opts.Values = []string{values}
	opts.Requirements = RequirementNames{
		PolicyConfigMap:   "acme-egress-v7",
		RailsConfigMap:    "acme-rails-v12",
		PresidioKeySecret: "acme-presidio-key",
		SamplerKeySecret:  "acme-sampler-key",
	}
	commands := highRiskCommands(opts, commandResponse{out: CommandOutput{Stdout: highRiskRenderedProof()}})
	report := Run(context.Background(), opts, Dependencies{
		Commands: commands,
		Platform: fakePlatform{os: "linux", arch: "amd64"},
		HTTP:     &recordingHTTP{status: http.StatusOK},
	})

	if report.Summary.FailedRequired != 0 {
		t.Fatalf("required failures = %d", report.Summary.FailedRequired)
	}
	for _, name := range []string{"acme-egress-v7", "acme-rails-v12", "acme-presidio-key", "acme-sampler-key"} {
		found := false
		for _, call := range commands.calls {
			if strings.Contains(call, name) {
				found = true
			}
		}
		if !found {
			t.Fatalf("override %q was not checked", name)
		}
	}
}

func TestEndpointCredentialsCannotEnterReport(t *testing.T) {
	deps, httpClient := noToolDependencies()
	opts := defaultOptions()
	opts.Endpoint = "https://collector.example/v1/traces?token=super-secret"
	report := Run(context.Background(), opts, deps)

	var output bytes.Buffer
	if err := Render(&output, report, "json"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "super-secret") {
		t.Fatal("report leaked endpoint query value")
	}
	if resultByCode(t, report, "DESTINATION-URL-001").Status != StatusFail {
		t.Fatal("credential-like query should fail validation")
	}
	if httpClient.called {
		t.Fatal("invalid URL should not be probed")
	}
}

func TestExplicitUnreachableEndpointIsRequired(t *testing.T) {
	deps, httpClient := noToolDependencies()
	opts := defaultOptions()
	opts.Endpoint = "https://collector.example"
	report := Run(context.Background(), opts, deps)

	if !httpClient.called {
		t.Fatal("valid explicit endpoint should be probed")
	}
	got := resultByCode(t, report, "DESTINATION-REACHABILITY-001")
	if got.Status != StatusFail || !got.Required || report.Summary.FailedRequired != 1 {
		t.Fatalf("reachability = %s required=%t total=%d", got.Status, got.Required, report.Summary.FailedRequired)
	}
}

func TestUnsupportedPlatformFailsRequiredCheck(t *testing.T) {
	deps, _ := noToolDependencies()
	deps.Platform = fakePlatform{os: "windows", arch: "amd64"}
	report := Run(context.Background(), defaultOptions(), deps)
	got := resultByCode(t, report, "PLATFORM-001")
	if got.Status != StatusFail || !got.Required {
		t.Fatalf("platform = %s required=%t", got.Status, got.Required)
	}
}

func TestOptionsValidation(t *testing.T) {
	tests := []Options{
		{Profile: "unknown", Namespace: "fabric-system", Timeout: time.Second},
		{Profile: "unprofiled", Namespace: "Invalid_Name", Timeout: time.Second},
		{Profile: "unprofiled", Namespace: "fabric-system", Timeout: 0},
		{Profile: "unprofiled", Namespace: "fabric-system", Timeout: 6 * time.Minute},
		{Profile: "unprofiled", Namespace: "fabric-system", Timeout: time.Second, Requirements: RequirementNames{RailsConfigMap: "INVALID_NAME"}},
	}
	for _, opts := range tests {
		if err := opts.Validate(); err == nil {
			t.Fatalf("Validate(%+v) succeeded", opts)
		}
	}
}

func TestLocalChartInputsRejectNetworkAndStdin(t *testing.T) {
	chart, values := localChartInputs(t)
	for name, candidate := range map[string]struct {
		chart  string
		values []string
	}{
		"remote chart":  {chart: "https://charts.example/fabric.tgz", values: []string{values}},
		"remote values": {chart: chart, values: []string{"https://config.example/values.yaml"}},
		"stdin values":  {chart: chart, values: []string{"-"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLocalChartInput(candidate.chart, candidate.values); err == nil {
				t.Fatal("unsafe input was accepted")
			}
		})
	}
}

func TestJSONContractUsesArraysForEmptyEvidence(t *testing.T) {
	report := newReport(defaultOptions(), []Result{result("CHECK-001", SeverityWarning, StatusWarn, false, "summary", "fix")})
	var output bytes.Buffer
	if err := Render(&output, report, "json"); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"schema_version": "fabricctl.doctor.v1"`, `"evidence": []`, `"failed_required": 0`} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("JSON missing %s:\n%s", expected, output.String())
		}
	}
}

func TestHumanOutputContract(t *testing.T) {
	report := newReport(defaultOptions(), []Result{
		result("CHECK-001", SeverityInfo, StatusPass, true, "A required check passed", "No action required.", "mode=test"),
		result("CHECK-002", SeverityWarning, StatusWarn, false, "An advisory check warned", "Review the environment."),
	})
	var output bytes.Buffer
	if err := Render(&output, report, "human"); err != nil {
		t.Fatal(err)
	}
	want := "SingleAxis Fabric preflight\n" +
		"Profile: unprofiled  Namespace: fabric-system\n\n" +
		"[PASS] CHECK-001                          A required check passed\n" +
		"       Evidence: mode=test\n" +
		"[WARN] CHECK-002                          An advisory check warned\n" +
		"       Remediation: Review the environment.\n\n" +
		"Summary: 1 passed, 1 warnings, 0 failed, 0 skipped; 0 required failures\n"
	if output.String() != want {
		t.Fatalf("human output changed:\n--- got ---\n%s--- want ---\n%s", output.String(), want)
	}
}
