// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package doctor

import (
	"fmt"
	"regexp"
	"time"
)

const SchemaVersion = "fabricctl.doctor.v1"

type Options struct {
	Profile      string
	Namespace    string
	Endpoint     string
	Chart        string
	Values       []string
	Timeout      time.Duration
	Version      string
	Requirements RequirementNames
}

type RequirementNames struct {
	PolicyConfigMap   string
	RailsConfigMap    string
	PresidioKeySecret string
	SamplerKeySecret  string
}

var namespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
var resourceNamePattern = regexp.MustCompile(`^[a-z0-9]([-.a-z0-9]*[a-z0-9])?$`)

func (o Options) Validate() error {
	if _, ok := profileByName(o.Profile); !ok {
		return fmt.Errorf("unsupported profile %q", o.Profile)
	}
	if len(o.Namespace) == 0 || len(o.Namespace) > 63 || !namespacePattern.MatchString(o.Namespace) {
		return fmt.Errorf("namespace %q is not a valid DNS label", o.Namespace)
	}
	if o.Timeout <= 0 || o.Timeout > 5*time.Minute {
		return fmt.Errorf("timeout must be greater than zero and no more than 5m")
	}
	for label, name := range map[string]string{
		"policy ConfigMap":    o.Requirements.PolicyConfigMap,
		"rails ConfigMap":     o.Requirements.RailsConfigMap,
		"Presidio key Secret": o.Requirements.PresidioKeySecret,
		"sampler key Secret":  o.Requirements.SamplerKeySecret,
	} {
		if name != "" && (len(name) > 253 || !resourceNamePattern.MatchString(name)) {
			return fmt.Errorf("%s name %q is not a valid Kubernetes resource name", label, name)
		}
	}
	return nil
}

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

type Result struct {
	Code        string   `json:"code"`
	Severity    Severity `json:"severity"`
	Status      Status   `json:"status"`
	Required    bool     `json:"required"`
	Summary     string   `json:"summary"`
	Remediation string   `json:"remediation"`
	Evidence    []string `json:"evidence"`
}

type Summary struct {
	Passed         int `json:"passed"`
	Warnings       int `json:"warnings"`
	Failed         int `json:"failed"`
	Skipped        int `json:"skipped"`
	FailedRequired int `json:"failed_required"`
}

type Report struct {
	SchemaVersion string   `json:"schema_version"`
	Fabricctl     string   `json:"fabricctl_version"`
	Profile       string   `json:"profile"`
	Namespace     string   `json:"namespace"`
	Summary       Summary  `json:"summary"`
	Results       []Result `json:"results"`
}

func newReport(opts Options, results []Result) Report {
	r := Report{
		SchemaVersion: SchemaVersion,
		Fabricctl:     opts.Version,
		Profile:       opts.Profile,
		Namespace:     opts.Namespace,
		Results:       results,
	}
	for _, result := range results {
		switch result.Status {
		case StatusPass:
			r.Summary.Passed++
		case StatusWarn:
			r.Summary.Warnings++
		case StatusFail:
			r.Summary.Failed++
			if result.Required {
				r.Summary.FailedRequired++
			}
		case StatusSkip:
			r.Summary.Skipped++
		}
	}
	return r
}

func result(code string, severity Severity, status Status, required bool, summary, remediation string, evidence ...string) Result {
	if evidence == nil {
		evidence = []string{}
	}
	return Result{Code: code, Severity: severity, Status: status, Required: required, Summary: summary, Remediation: remediation, Evidence: evidence}
}
