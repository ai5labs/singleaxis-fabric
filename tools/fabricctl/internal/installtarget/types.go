// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

// Package installtarget implements offline inspection of the
// FabricInstallTarget v1alpha1 desired-state contract. It never resolves
// references, contacts a cluster, or mutates an installation.
package installtarget

const (
	APIVersion       = "fabric.singleaxis.dev/v1alpha1"
	Kind             = "FabricInstallTarget"
	MaxDocumentBytes = 1_048_576

	ProfilePermissiveDev = "permissive-dev"
	ProfileHighRisk      = "eu-ai-act-high-risk"
)

// Diagnostic is a stable, value-free validation result suitable for logs.
type Diagnostic struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Summary  string `json:"summary"`
}

// DocumentError reports a safe-loading failure before validation can begin.
type DocumentError struct{ Diagnostic Diagnostic }

func (e *DocumentError) Error() string { return e.Diagnostic.Summary }

// Parsed retains the generic decoded document used for digest identity and
// its strictly validated typed representation.
type Parsed struct {
	Document any
	Resource Resource
}

type Resource struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind" yaml:"kind"`
	Metadata   Metadata `json:"metadata" yaml:"metadata"`
	Spec       Spec     `json:"spec" yaml:"spec"`
}

type Metadata struct {
	Name string `json:"name" yaml:"name"`
}

type Spec struct {
	DeploymentRef DeploymentRef `json:"deploymentRef" yaml:"deploymentRef"`
	Distribution  Distribution  `json:"distribution" yaml:"distribution"`
	Profile       Profile       `json:"profile" yaml:"profile"`
	Backend       Backend       `json:"backend" yaml:"backend"`
	Bindings      *Bindings     `json:"bindings,omitempty" yaml:"bindings,omitempty"`
}

type DeploymentRef struct {
	Name   string `json:"name" yaml:"name"`
	Digest string `json:"digest" yaml:"digest"`
}

type Distribution struct {
	ArtifactRef string `json:"artifactRef" yaml:"artifactRef"`
	Version     string `json:"version" yaml:"version"`
	Digest      string `json:"digest" yaml:"digest"`
}

type Profile struct {
	Name   string `json:"name" yaml:"name"`
	Digest string `json:"digest" yaml:"digest"`
}

type Backend struct {
	Type string     `json:"type" yaml:"type"`
	Helm HelmTarget `json:"helm" yaml:"helm"`
}

type HelmTarget struct {
	Context         string `json:"context" yaml:"context"`
	Namespace       string `json:"namespace" yaml:"namespace"`
	ReleaseName     string `json:"releaseName" yaml:"releaseName"`
	CreateNamespace bool   `json:"createNamespace" yaml:"createNamespace"`
}

type Bindings struct {
	TenantID    string      `json:"tenantId" yaml:"tenantId"`
	Exporter    Exporter    `json:"exporter" yaml:"exporter"`
	UpdateTrust UpdateTrust `json:"updateTrust" yaml:"updateTrust"`
}

type Exporter struct {
	Endpoint string `json:"endpoint" yaml:"endpoint"`
	Egress   Egress `json:"egress" yaml:"egress"`
}

type Egress struct {
	CIDRs []string `json:"cidrs" yaml:"cidrs"`
	Ports []Port   `json:"ports" yaml:"ports"`
}

type Port struct {
	Protocol string `json:"protocol" yaml:"protocol"`
	Port     int    `json:"port" yaml:"port"`
}

type UpdateTrust struct {
	KeyID     string `json:"keyId" yaml:"keyId"`
	PublicKey string `json:"publicKey" yaml:"publicKey"`
}
