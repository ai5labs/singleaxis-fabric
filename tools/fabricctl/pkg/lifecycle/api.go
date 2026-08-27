// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

// Package lifecycle is the versioned public API for Fabric desired-state and
// lifecycle consumers. The fabricctl CLI, a self-hosted controller, and the
// SingleAxis platform must use this package instead of implementing bundle
// identity or verification independently.
package lifecycle

import (
	"fmt"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/bundle"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/installtarget"
)

const (
	// APIVersion identifies this Go facade. Contract documents retain their own
	// schema versions so they can evolve independently of the Go module.
	APIVersion = "lifecycle.fabric.singleaxis.dev/v1alpha1"

	DeploymentFileName       = bundle.DeploymentFileName
	InstallTargetFileName    = bundle.InstallTargetFileName
	ValuesFileName           = bundle.ValuesFileName
	SecretsRequiredFileName  = bundle.SecretsRequiredFileName
	InstallationPlanFileName = bundle.InstallationPlanFileName
	ManifestFileName         = bundle.ManifestFileName
)

// Generator is immutable release identity for the bundle producer.
type Generator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// Artifact contains exact bytes for one allowlisted bundle artifact.
type Artifact struct {
	Path    string `json:"path"`
	Payload []byte `json:"-"`
}

// Bundle is the deterministic, portable result shared by every frontend.
type Bundle struct {
	Artifacts    []Artifact `json:"artifacts"`
	BundleDigest string     `json:"bundle_digest"`
}

// Payload returns a defensive copy of an artifact.
func (b Bundle) Payload(path string) ([]byte, error) {
	for _, artifact := range b.Artifacts {
		if artifact.Path == path {
			return append([]byte(nil), artifact.Payload...), nil
		}
	}
	return nil, fmt.Errorf("bundle artifact is not present")
}

// Paths returns the stable bundle artifact order.
func (b Bundle) Paths() []string {
	paths := make([]string, 0, len(b.Artifacts))
	for _, artifact := range b.Artifacts {
		paths = append(paths, artifact.Path)
	}
	return paths
}

// Diagnostic is stable, value-free, and suitable for logs or UI rendering.
type Diagnostic struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Path     string `json:"path,omitempty"`
	Summary  string `json:"summary"`
}

// BuildBundle validates raw public contract bytes and builds one deterministic
// Offline Install Bundle. It performs no network, registry, cluster, platform,
// process, or secret-store access.
func BuildBundle(deploymentBytes []byte, deploymentFormat string, targetBytes []byte, targetFormat string, generator Generator) (Bundle, []Diagnostic, error) {
	deploymentDocument, err := deployment.LoadBytes(deploymentBytes, deploymentFormat)
	if err != nil {
		return Bundle{}, nil, fmt.Errorf("deployment document cannot be safely decoded")
	}
	deploymentResource, deploymentDiagnostics := deployment.Validate(deploymentDocument)
	if len(deploymentDiagnostics) != 0 || deploymentResource == nil {
		return Bundle{}, convertDeploymentDiagnostics(deploymentDiagnostics), nil
	}

	targetDocument, err := installtarget.LoadBytes(targetBytes, targetFormat)
	if err != nil {
		return Bundle{}, nil, fmt.Errorf("install-target document cannot be safely decoded")
	}
	targetResource, targetDiagnostics := installtarget.Validate(targetDocument)
	if len(targetDiagnostics) != 0 || targetResource == nil {
		return Bundle{}, convertTargetDiagnostics(targetDiagnostics), nil
	}
	bindingDiagnostics := installtarget.ValidateAgainstDeployment(*targetResource, *deploymentResource)
	if len(bindingDiagnostics) != 0 {
		return Bundle{}, convertTargetDiagnostics(bindingDiagnostics), nil
	}

	built, err := bundle.Build(*deploymentResource, *targetResource, bundle.Generator{
		Name: generator.Name, Version: generator.Version, Commit: generator.Commit,
	})
	if err != nil {
		return Bundle{}, nil, fmt.Errorf("build verified bundle: %w", err)
	}
	result := Bundle{BundleDigest: built.BundleDigest, Artifacts: make([]Artifact, 0, len(built.Artifacts))}
	for _, artifact := range built.Artifacts {
		result.Artifacts = append(result.Artifacts, Artifact{Path: artifact.Path, Payload: append([]byte(nil), artifact.Payload...)})
	}
	return result, nil, nil
}

// VerificationEffect declares whether a verification operation has effects.
type VerificationEffect struct {
	Network  bool `json:"network"`
	Mutating bool `json:"mutating"`
}

type Check struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// VerificationReport is a value-free public representation of local bundle
// verification. Pass means internal consistency, never runtime readiness.
type VerificationReport struct {
	SchemaVersion string             `json:"schema_version"`
	Scope         string             `json:"scope"`
	Status        string             `json:"status"`
	Readiness     string             `json:"readiness"`
	Operation     VerificationEffect `json:"operation"`
	BundleDigest  string             `json:"bundle_digest,omitempty"`
	Checks        []Check            `json:"checks"`
	Diagnostics   []Diagnostic       `json:"diagnostics"`
}

// VerifyBundleDirectory verifies exactly one Offline Install Bundle locally.
func VerifyBundleDirectory(dir string) VerificationReport {
	report := bundle.VerifyDirectory(dir)
	result := VerificationReport{
		SchemaVersion: report.SchemaVersion,
		Scope:         report.Scope,
		Status:        report.Status,
		Readiness:     report.Readiness,
		Operation:     VerificationEffect{Network: report.Operation.Network, Mutating: report.Operation.Mutating},
		BundleDigest:  report.BundleDigest,
		Checks:        make([]Check, 0, len(report.Checks)),
		Diagnostics:   make([]Diagnostic, 0, len(report.Diagnostics)),
	}
	for _, check := range report.Checks {
		result.Checks = append(result.Checks, Check{ID: check.ID, Status: check.Status})
	}
	for _, diagnostic := range report.Diagnostics {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{ID: diagnostic.ID, Severity: diagnostic.Severity, Summary: diagnostic.Summary})
	}
	return result
}

func convertDeploymentDiagnostics(source []deployment.Diagnostic) []Diagnostic {
	result := make([]Diagnostic, 0, len(source))
	for _, diagnostic := range source {
		result = append(result, Diagnostic{ID: diagnostic.ID, Severity: diagnostic.Severity, Path: diagnostic.Path, Summary: diagnostic.Summary})
	}
	return result
}

func convertTargetDiagnostics(source []installtarget.Diagnostic) []Diagnostic {
	result := make([]Diagnostic, 0, len(source))
	for _, diagnostic := range source {
		result = append(result, Diagnostic{ID: diagnostic.ID, Severity: diagnostic.Severity, Path: diagnostic.Path, Summary: diagnostic.Summary})
	}
	return result
}
