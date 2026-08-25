// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

// Package deployment implements offline inspection of the public
// FabricDeployment v1alpha1 contract. It deliberately does not resolve
// references, contact a platform, or mutate a runtime.
package deployment

const (
	APIVersion       = "fabric.singleaxis.dev/v1alpha1"
	Kind             = "FabricDeployment"
	MaxDocumentBytes = 1_048_576
)

// Diagnostic is stable and safe to put in logs. Summaries never include input
// values, file names, or parser implementation details.
type Diagnostic struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Path     string `json:"path"`
	Summary  string `json:"summary"`
}

// DocumentError reports a failure before contract validation can start.
type DocumentError struct {
	Diagnostic Diagnostic
}

func (e *DocumentError) Error() string { return e.Diagnostic.Summary }

// Parsed retains the complete decoded input for digest identity while also
// exposing its strictly validated typed form.
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
	AssuranceLevel string     `json:"assuranceLevel" yaml:"assuranceLevel"`
	Connection     Connection `json:"connection" yaml:"connection"`
	Controls       *Controls  `json:"controls,omitempty" yaml:"controls,omitempty"`
	Observe        Observe    `json:"observe" yaml:"observe"`
	Assurance      *Assurance `json:"assurance,omitempty" yaml:"assurance,omitempty"`
	Rollout        *Rollout   `json:"rollout,omitempty" yaml:"rollout,omitempty"`
}

type Connection struct {
	Mode                string `json:"mode" yaml:"mode"`
	TenantIDFrom        string `json:"tenantIdFrom" yaml:"tenantIdFrom"`
	WorkloadIdentityRef string `json:"workloadIdentityRef,omitempty" yaml:"workloadIdentityRef,omitempty"`
}

type Controls struct {
	ProfileRef       string `json:"profileRef" yaml:"profileRef"`
	PolicyRef        string `json:"policyRef,omitempty" yaml:"policyRef,omitempty"`
	AuthorizationRef string `json:"authorizationRef,omitempty" yaml:"authorizationRef,omitempty"`
	PIIRef           string `json:"piiRef,omitempty" yaml:"piiRef,omitempty"`
	GuardrailRef     string `json:"guardrailRef,omitempty" yaml:"guardrailRef,omitempty"`
	EscalationRef    string `json:"escalationRef,omitempty" yaml:"escalationRef,omitempty"`
}

type Observe struct {
	ContentMode string `json:"contentMode" yaml:"contentMode"`
	RelayRef    string `json:"relayRef,omitempty" yaml:"relayRef,omitempty"`
}

type Assurance struct {
	PlanRef string `json:"planRef" yaml:"planRef"`
}

type Rollout struct {
	ApprovalRef string `json:"approvalRef" yaml:"approvalRef"`
}

type PlanRole struct {
	ID       string `json:"id"`
	Plane    string `json:"plane"`
	Artifact string `json:"artifact"`
	Purpose  string `json:"purpose"`
}

type PlanReference struct {
	ID        string `json:"id"`
	Field     string `json:"field"`
	Reference string `json:"reference"`
}

type PlanPrerequisite struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type PlanIntegration struct {
	Mode     string `json:"mode"`
	Artifact string `json:"artifact"`
}

// Plan is deterministic and descriptive. It is not an attestation or an
// apply/reconcile operation.
type Plan struct {
	AssuranceLevel string             `json:"assurance_level"`
	Integration    PlanIntegration    `json:"integration"`
	Roles          []PlanRole         `json:"roles"`
	References     []PlanReference    `json:"references"`
	Prerequisites  []PlanPrerequisite `json:"prerequisites"`
}
