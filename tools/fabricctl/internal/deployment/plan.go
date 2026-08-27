// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package deployment

var integrationArtifacts = map[string]string{
	"sdk":     "Fabric SDK",
	"adapter": "Fabric Adapter",
	"gateway": "Fabric Gateway",
	"otlp":    "Fabric Collector OTLP receiver",
}

// BuildPlan selects OSS roles and unverified operator prerequisites without
// reading the environment or resolving a single reference.
func BuildPlan(resource Resource) Plan {
	plan := Plan{
		AssuranceLevel: resource.Spec.AssuranceLevel,
		Integration: PlanIntegration{
			Mode:     resource.Spec.Connection.Mode,
			Artifact: integrationArtifacts[resource.Spec.Connection.Mode],
		},
		Roles: []PlanRole{{
			ID: "deployment.role.connect", Plane: "connect",
			Artifact: integrationArtifacts[resource.Spec.Connection.Mode],
			Purpose:  "Integrate the agent and propagate stable Fabric identity",
		}},
		References: []PlanReference{{
			ID: "deployment.reference.tenant_identity", Field: "spec.connection.tenantIdFrom",
			Reference: resource.Spec.Connection.TenantIDFrom,
		}},
		Prerequisites: []PlanPrerequisite{
			required("deployment.prerequisite.tenant_identity_authorized", "Authorize the selected runtime to use the referenced tenant identity"),
			required("deployment.prerequisite.connection."+resource.Spec.Connection.Mode, "Install and configure the selected connection artifact at the agent boundary"),
			required("deployment.prerequisite.collector_ready", "Provide a reachable Fabric Collector with privacy and delivery health configured"),
			required("deployment.prerequisite.content_mode."+resource.Spec.Observe.ContentMode, "Confirm the selected Observe content mode is approved for this deployment"),
		},
	}

	if value := resource.Spec.Connection.WorkloadIdentityRef; value != "" {
		plan.References = append(plan.References, PlanReference{
			ID: "deployment.reference.workload_identity", Field: "spec.connection.workloadIdentityRef", Reference: value,
		})
		plan.Prerequisites = append(plan.Prerequisites, required(
			"deployment.prerequisite.workload_identity_bound",
			"Bind and authorize the referenced workload identity independently of telemetry",
		))
	}
	if controls := resource.Spec.Controls; controls != nil {
		plan.Roles = append(plan.Roles, PlanRole{
			ID: "deployment.role.control", Plane: "control", Artifact: "Fabric Control integration",
			Purpose: "Enforce only the referenced runtime control profile and bindings",
		})
		appendReference(&plan, "deployment.reference.control_profile", "spec.controls.profileRef", controls.ProfileRef)
		appendReference(&plan, "deployment.reference.policy", "spec.controls.policyRef", controls.PolicyRef)
		appendReference(&plan, "deployment.reference.authorization", "spec.controls.authorizationRef", controls.AuthorizationRef)
		appendReference(&plan, "deployment.reference.pii", "spec.controls.piiRef", controls.PIIRef)
		appendReference(&plan, "deployment.reference.guardrail", "spec.controls.guardrailRef", controls.GuardrailRef)
		appendReference(&plan, "deployment.reference.escalation", "spec.controls.escalationRef", controls.EscalationRef)
		plan.Prerequisites = append(plan.Prerequisites,
			required("deployment.prerequisite.control_profile_available", "Make the referenced control profile available at the enforcement point"),
			required("deployment.prerequisite.control_failure_posture_reviewed", "Review timeout, bypass, fail-open, and fail-closed behavior for every control"),
		)
	}

	plan.Roles = append(plan.Roles, PlanRole{
		ID: "deployment.role.collector", Plane: "observe", Artifact: "Fabric Collector",
		Purpose: "Receive, normalize, privacy-process, correlate, and route telemetry",
	})
	if value := resource.Spec.Observe.RelayRef; value != "" {
		plan.Roles = append(plan.Roles, PlanRole{
			ID: "deployment.role.relay", Plane: "observe", Artifact: "Fabric Relay",
			Purpose: "Durably export approved telemetry to the referenced destination",
		})
		appendReference(&plan, "deployment.reference.relay", "spec.observe.relayRef", value)
		plan.Prerequisites = append(plan.Prerequisites, required(
			"deployment.prerequisite.relay_ready",
			"Provision the referenced Relay with authenticated export and delivery monitoring",
		))
	}
	if assurance := resource.Spec.Assurance; assurance != nil {
		plan.Roles = append(plan.Roles, PlanRole{
			ID: "deployment.role.assurance_runner", Plane: "assurance", Artifact: "Fabric Assurance Runner",
			Purpose: "Run the referenced assurance plan outside the agent request path",
		})
		appendReference(&plan, "deployment.reference.assurance_plan", "spec.assurance.planRef", assurance.PlanRef)
		plan.Prerequisites = append(plan.Prerequisites, required(
			"deployment.prerequisite.assurance_plan_qualified",
			"Run and review the referenced assurance plan before rollout",
		))
	}
	if rollout := resource.Spec.Rollout; rollout != nil {
		appendReference(&plan, "deployment.reference.rollout_approval", "spec.rollout.approvalRef", rollout.ApprovalRef)
		plan.Prerequisites = append(plan.Prerequisites, required(
			"deployment.prerequisite.rollout_approval_verified",
			"Independently verify the referenced approval before any rollout",
		))
	}

	levelPrerequisites := map[string]PlanPrerequisite{
		"A0": required("deployment.prerequisite.a0.synthetic_data", "Keep development use limited to approved synthetic or non-sensitive data"),
		"A1": required("deployment.prerequisite.a1.delivery_monitoring", "Establish operator ownership for authenticated export and delivery-loss alerts"),
		"A2": required("deployment.prerequisite.a2.incident_readiness", "Document incident retention and recovery ownership for the controlled deployment"),
		"A3": required("deployment.prerequisite.a3.separation_of_duties", "Establish separation of duties for policy, approval, operation, and investigation"),
	}
	plan.Prerequisites = append(plan.Prerequisites, levelPrerequisites[resource.Spec.AssuranceLevel])
	if resource.Spec.AssuranceLevel == "A3" {
		plan.Prerequisites = append(plan.Prerequisites, required(
			"deployment.prerequisite.a3.recovery_evidence",
			"Test recovery and retain reconstruction evidence in a customer-approved destination",
		))
	}
	return plan
}

func required(id, summary string) PlanPrerequisite {
	return PlanPrerequisite{ID: id, Status: "required", Summary: summary}
}

func appendReference(plan *Plan, id, field, reference string) {
	if reference != "" {
		plan.References = append(plan.References, PlanReference{ID: id, Field: field, Reference: reference})
	}
}
