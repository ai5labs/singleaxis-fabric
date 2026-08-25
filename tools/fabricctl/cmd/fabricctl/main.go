package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/doctor"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintf(stdout, "fabricctl %s (commit %s, built %s)\n", version, commit, buildDate)
		return 0
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var valuesFiles stringList
	profile := fs.String("profile", "unprofiled", "built-in profile: unprofiled, permissive-dev, or eu-ai-act-high-risk")
	namespace := fs.String("namespace", "fabric-system", "Kubernetes namespace to inspect")
	endpoint := fs.String("endpoint", "", "optional telemetry destination URL to validate and probe")
	chart := fs.String("chart", "", "local Fabric Helm chart directory or archive")
	fs.Var(&valuesFiles, "values", "local Helm values file; repeat for ordered overlays")
	policyConfigMap := fs.String("policy-configmap", "fabric-high-risk-egress-policy", "approved policy ConfigMap name")
	railsConfigMap := fs.String("rails-configmap", "fabric-high-risk-rails", "approved rails ConfigMap name")
	presidioKeySecret := fs.String("presidio-key-secret", "fabric-presidio-tenant-key", "Presidio tenant key Secret name")
	samplerKeySecret := fs.String("sampler-key-secret", "fabric-otel-sampler-key", "sampler HMAC key Secret name")
	output := fs.String("output", "human", "output format: human or json")
	timeout := fs.Duration("timeout", 5*time.Second, "timeout per external check")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: fabricctl doctor [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "doctor does not accept positional arguments: %v\n", fs.Args())
		return 2
	}

	opts := doctor.Options{
		Profile:   *profile,
		Namespace: *namespace,
		Endpoint:  *endpoint,
		Chart:     *chart,
		Values:    []string(valuesFiles),
		Timeout:   *timeout,
		Version:   version,
		Requirements: doctor.RequirementNames{
			PolicyConfigMap:   *policyConfigMap,
			RailsConfigMap:    *railsConfigMap,
			PresidioKeySecret: *presidioKeySecret,
			SamplerKeySecret:  *samplerKeySecret,
		},
	}
	if err := opts.Validate(); err != nil {
		fmt.Fprintf(stderr, "invalid doctor configuration: %v\n", err)
		return 2
	}
	if *output != "human" && *output != "json" {
		fmt.Fprintf(stderr, "invalid --output %q: must be human or json\n", *output)
		return 2
	}

	report := doctor.Run(context.Background(), opts, doctor.SystemDependencies())
	if err := doctor.Render(stdout, report, *output); err != nil {
		fmt.Fprintf(stderr, "render doctor report: %v\n", err)
		return 1
	}
	if report.Summary.FailedRequired > 0 {
		return 1
	}
	return 0
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "fabricctl securely preflights a SingleAxis Fabric deployment.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  fabricctl doctor [flags]")
	fmt.Fprintln(w, "  fabricctl version")
}
