# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Validation and deterministic identity for ``FabricDeployment`` resources.

This module deliberately stops at local inspection.  It does not resolve
references, contact a management service, or apply desired state.
"""

from __future__ import annotations

import hashlib
import json
import re
from dataclasses import asdict, dataclass
from pathlib import Path
from typing import Any, Literal

import yaml
from pydantic import BaseModel, ConfigDict, Field, ValidationError, model_validator

API_VERSION = "fabric.singleaxis.dev/v1alpha1"
KIND = "FabricDeployment"
MAX_DOCUMENT_BYTES = 1_048_576

_NAME_PATTERN = re.compile(r"^[a-z0-9](?:[a-z0-9.-]{0,61}[a-z0-9])?$")
_REFERENCE_PATTERN = re.compile(r"^[A-Za-z0-9](?:[A-Za-z0-9._:/-]{0,251}[A-Za-z0-9])?$")
_OPAQUE_REFERENCE_PATTERN = re.compile(r"^[A-Za-z0-9_-]{48,}$")
_HEX_REFERENCE_PATTERN = re.compile(r"^[A-Fa-f0-9]{40,}$")
_CREDENTIAL_REFERENCE_PATTERNS = (
    re.compile(r"^(?:bearer[ :]?)[A-Za-z0-9._~+/-]+$", re.IGNORECASE),
    re.compile(r"^(?:sk|pk|api[_-]?key|token|secret)[_-][A-Za-z0-9._~+/-]{8,}$", re.IGNORECASE),
    re.compile(r"^(?:AKIA|ASIA)[A-Z0-9]{16}$"),
    re.compile(r"^gh[pousr]_[A-Za-z0-9]{20,}$"),
    re.compile(r"^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$"),
)
_FORBIDDEN_KEY_PATTERN = re.compile(
    r"^(?:env|envs|envvars|environmentvariables|password|passwd|secret|token|"
    r"apikey|api_key|credential|credentials)$",
    re.IGNORECASE,
)


@dataclass(frozen=True)
class DeploymentDiagnostic:
    """A stable, value-free diagnostic suitable for automation and audit logs."""

    id: str
    severity: Literal["error"]
    path: str
    summary: str


class DeploymentDocumentError(ValueError):
    """A document could not be safely decoded into a deployment resource."""

    def __init__(self, diagnostic: DeploymentDiagnostic) -> None:
        super().__init__(diagnostic.summary)
        self.diagnostic = diagnostic


class _StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True, populate_by_name=True)


class DeploymentMetadata(_StrictModel):
    name: str


class DeploymentConnection(_StrictModel):
    mode: Literal["sdk", "adapter", "gateway", "otlp"]
    tenant_id_from: str = Field(alias="tenantIdFrom")
    workload_identity_ref: str | None = Field(default=None, alias="workloadIdentityRef")


class DeploymentControls(_StrictModel):
    profile_ref: str = Field(alias="profileRef")
    policy_ref: str | None = Field(default=None, alias="policyRef")
    authorization_ref: str | None = Field(default=None, alias="authorizationRef")
    pii_ref: str | None = Field(default=None, alias="piiRef")
    guardrail_ref: str | None = Field(default=None, alias="guardrailRef")
    escalation_ref: str | None = Field(default=None, alias="escalationRef")


class DeploymentObserve(_StrictModel):
    content_mode: Literal["metadata-only", "hash-only", "content-ref"] = Field(alias="contentMode")
    relay_ref: str | None = Field(default=None, alias="relayRef")


class DeploymentAssurance(_StrictModel):
    plan_ref: str = Field(alias="planRef")


class DeploymentRollout(_StrictModel):
    approval_ref: str = Field(alias="approvalRef")


class DeploymentSpec(_StrictModel):
    assurance_level: Literal["A0", "A1", "A2", "A3"] = Field(alias="assuranceLevel")
    connection: DeploymentConnection
    controls: DeploymentControls | None = None
    observe: DeploymentObserve
    assurance: DeploymentAssurance | None = None
    rollout: DeploymentRollout | None = None

    @model_validator(mode="after")
    def validate_assurance_requirements(self) -> DeploymentSpec:
        if self.assurance_level in {"A1", "A2", "A3"} and not self.observe.relay_ref:
            raise ValueError("A1-A3 require observe.relayRef")
        if self.assurance_level == "A3" and not self.connection.workload_identity_ref:
            raise ValueError("A3 requires connection.workloadIdentityRef")
        if self.assurance_level in {"A2", "A3"}:
            if self.controls is None:
                raise ValueError("A2-A3 require controls.profileRef")
            if self.assurance is None:
                raise ValueError("A2-A3 require assurance.planRef")
            if self.rollout is None:
                raise ValueError("A2-A3 require rollout.approvalRef")
        return self


class FabricDeployment(BaseModel):
    """The v1alpha1 local desired-state model.

    References stay opaque at this layer.  A future controller may resolve
    them, but local validation neither assumes nor requires SingleAxis.
    """

    model_config = ConfigDict(extra="forbid", strict=True, populate_by_name=True)

    api_version: Literal["fabric.singleaxis.dev/v1alpha1"] = Field(alias="apiVersion")
    kind: Literal["FabricDeployment"]
    metadata: DeploymentMetadata
    spec: DeploymentSpec


@dataclass(frozen=True)
class DeploymentPlanRole:
    """One selected public runtime or readiness role."""

    id: str
    plane: Literal["connect", "control", "observe", "assurance"]
    artifact: str
    purpose: str


@dataclass(frozen=True)
class DeploymentPlanReference:
    """One opaque desired-state reference; the planner never resolves it."""

    id: str
    field: str
    reference: str


@dataclass(frozen=True)
class DeploymentPlanPrerequisite:
    """One unverified operator prerequisite with a stable automation ID."""

    id: str
    status: Literal["required"]
    summary: str


@dataclass(frozen=True)
class DeploymentPlan:
    """Deterministic, non-mutating installation/readiness plan."""

    assurance_level: Literal["A0", "A1", "A2", "A3"]
    integration_mode: Literal["sdk", "adapter", "gateway", "otlp"]
    integration_artifact: str
    roles: tuple[DeploymentPlanRole, ...]
    references: tuple[DeploymentPlanReference, ...]
    prerequisites: tuple[DeploymentPlanPrerequisite, ...]


class _UniqueKeyLoader(yaml.SafeLoader):
    """Safe YAML loader that rejects mappings with duplicate keys."""


def _construct_unique_mapping(
    loader: _UniqueKeyLoader,
    node: yaml.nodes.MappingNode,
    deep: bool = False,
) -> dict[object, object]:
    loader.flatten_mapping(node)
    result: dict[object, object] = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        try:
            duplicate = key in result
        except TypeError as exc:
            raise yaml.constructor.ConstructorError(
                "while constructing a mapping",
                node.start_mark,
                "found an unhashable mapping key",
                key_node.start_mark,
            ) from exc
        if duplicate:
            raise yaml.constructor.ConstructorError(
                "while constructing a mapping",
                node.start_mark,
                "found duplicate mapping key",
                key_node.start_mark,
            )
        result[key] = loader.construct_object(value_node, deep=deep)
    return result


_UniqueKeyLoader.add_constructor(
    yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG,
    _construct_unique_mapping,
)


def _json_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate mapping key")
        result[key] = value
    return result


def _document_error(diagnostic_id: str, summary: str) -> DeploymentDocumentError:
    return DeploymentDocumentError(DeploymentDiagnostic(diagnostic_id, "error", "$", summary))


def _yaml_has_alias_or_anchor(raw: str) -> bool:
    """Inspect parser events without composing aliases into object graphs."""

    for event in yaml.parse(raw, Loader=_UniqueKeyLoader):
        if isinstance(event, yaml.events.AliasEvent) or getattr(event, "anchor", None):
            return True
    return False


def load_deployment(path: Path) -> Any:
    """Load a bounded YAML or JSON document without resolving external data."""

    try:
        size = path.stat().st_size
    except FileNotFoundError as exc:
        raise _document_error("deployment.file.not_found", "Deployment file was not found") from exc
    except OSError as exc:
        raise _document_error(
            "deployment.file.unreadable", "Deployment file cannot be read"
        ) from exc
    if size > MAX_DOCUMENT_BYTES:
        raise _document_error(
            "deployment.file.too_large",
            f"Deployment file exceeds the {MAX_DOCUMENT_BYTES}-byte limit",
        )
    try:
        raw = path.read_text(encoding="utf-8")
    except UnicodeDecodeError as exc:
        raise _document_error(
            "deployment.document.encoding", "Deployment file must be UTF-8"
        ) from exc
    except OSError as exc:
        raise _document_error(
            "deployment.file.unreadable", "Deployment file cannot be read"
        ) from exc
    if path.suffix.lower() == ".json":
        try:
            return json.loads(raw, object_pairs_hook=_json_object)
        except (json.JSONDecodeError, ValueError) as exc:
            raise _document_error(
                "deployment.document.syntax",
                "Deployment file contains invalid or duplicate syntax",
            ) from exc
    try:
        has_alias_or_anchor = _yaml_has_alias_or_anchor(raw)
    except yaml.YAMLError as exc:
        raise _document_error(
            "deployment.document.syntax",
            "Deployment file contains invalid or duplicate syntax",
        ) from exc
    if has_alias_or_anchor:
        raise _document_error(
            "deployment.document.alias_forbidden",
            "YAML anchors and aliases are forbidden in FabricDeployment files",
        )
    try:
        # _UniqueKeyLoader inherits SafeLoader and adds only duplicate-key rejection.
        return yaml.load(raw, Loader=_UniqueKeyLoader)  # noqa: S506
    except (yaml.YAMLError, ValueError) as exc:
        raise _document_error(
            "deployment.document.syntax", "Deployment file contains invalid or duplicate syntax"
        ) from exc


def _path(parts: tuple[str | int, ...]) -> str:
    rendered = "$"
    for part in parts:
        rendered += f"[{part}]" if isinstance(part, int) else f".{part}"
    return rendered


def _find_forbidden_keys(
    value: Any,
    path: tuple[str | int, ...] = (),
) -> list[DeploymentDiagnostic]:
    diagnostics: list[DeploymentDiagnostic] = []
    if isinstance(value, dict):
        for key, child in value.items():
            child_path = (*path, str(key))
            if isinstance(key, str) and _FORBIDDEN_KEY_PATTERN.fullmatch(key):
                diagnostics.append(
                    DeploymentDiagnostic(
                        "deployment.security.inline_sensitive_value",
                        "error",
                        _path(child_path),
                        "Inline secrets, credentials, tokens, and environment dumps are forbidden",
                    )
                )
            diagnostics.extend(_find_forbidden_keys(child, child_path))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            diagnostics.extend(_find_forbidden_keys(child, (*path, index)))
    return diagnostics


def _reference_diagnostics(resource: FabricDeployment) -> list[DeploymentDiagnostic]:
    diagnostics: list[DeploymentDiagnostic] = []
    if not _NAME_PATTERN.fullmatch(resource.metadata.name):
        diagnostics.append(
            DeploymentDiagnostic(
                "deployment.identity.invalid_name",
                "error",
                "$.metadata.name",
                "metadata.name must be a lowercase DNS-style name of at most 63 characters",
            )
        )
    values: list[tuple[str, str | None]] = [
        ("$.spec.connection.tenantIdFrom", resource.spec.connection.tenant_id_from),
        (
            "$.spec.connection.workloadIdentityRef",
            resource.spec.connection.workload_identity_ref,
        ),
        ("$.spec.observe.relayRef", resource.spec.observe.relay_ref),
    ]
    if resource.spec.controls is not None:
        controls = resource.spec.controls
        values.extend(
            [
                ("$.spec.controls.profileRef", controls.profile_ref),
                ("$.spec.controls.policyRef", controls.policy_ref),
                ("$.spec.controls.authorizationRef", controls.authorization_ref),
                ("$.spec.controls.piiRef", controls.pii_ref),
                ("$.spec.controls.guardrailRef", controls.guardrail_ref),
                ("$.spec.controls.escalationRef", controls.escalation_ref),
            ]
        )
    if resource.spec.assurance is not None:
        values.append(("$.spec.assurance.planRef", resource.spec.assurance.plan_ref))
    if resource.spec.rollout is not None:
        values.append(("$.spec.rollout.approvalRef", resource.spec.rollout.approval_ref))
    for path, value in values:
        if value is None:
            continue
        if not _REFERENCE_PATTERN.fullmatch(value):
            diagnostics.append(
                DeploymentDiagnostic(
                    "deployment.reference.invalid",
                    "error",
                    path,
                    "Reference must be 1-253 identifier characters and contain no inline value",
                )
            )
        elif _reference_looks_sensitive(value):
            diagnostics.append(
                DeploymentDiagnostic(
                    "deployment.reference.sensitive",
                    "error",
                    path,
                    "Reference resembles credential material; use an external reference "
                    "identifier instead",
                )
            )
    return diagnostics


def _reference_looks_sensitive(value: str) -> bool:
    """Reject common credentials and long opaque tokens without logging them."""

    if any(pattern.fullmatch(value) for pattern in _CREDENTIAL_REFERENCE_PATTERNS):
        return True
    return not any(separator in value for separator in "/:.") and bool(
        _OPAQUE_REFERENCE_PATTERN.fullmatch(value) or _HEX_REFERENCE_PATTERN.fullmatch(value)
    )


def validate_deployment(value: Any) -> tuple[FabricDeployment | None, list[DeploymentDiagnostic]]:
    """Validate a decoded document and return stable, value-free diagnostics."""

    if not isinstance(value, dict):
        return None, [
            DeploymentDiagnostic(
                "deployment.document.type",
                "error",
                "$",
                "Deployment document must be a mapping",
            )
        ]
    security = _find_forbidden_keys(value)
    if security:
        return None, security
    try:
        resource = FabricDeployment.model_validate(value)
    except ValidationError as exc:
        diagnostics: list[DeploymentDiagnostic] = []
        for error in exc.errors(include_url=False, include_input=False):
            error_type = str(error["type"])
            if error_type == "missing":
                diagnostic_id = "deployment.field.required"
                summary = "Required deployment field is missing"
            elif error_type == "extra_forbidden":
                diagnostic_id = "deployment.field.unknown"
                summary = "Unknown deployment field is forbidden"
            elif error_type in {"literal_error", "string_pattern_mismatch"}:
                diagnostic_id = "deployment.field.value"
                summary = "Deployment field has an unsupported value"
            elif error_type == "value_error":
                diagnostic_id = "deployment.assurance.requirements"
                summary = "Assurance level requirements are not satisfied"
            else:
                diagnostic_id = "deployment.field.type"
                summary = "Deployment field has the wrong type"
            location = tuple(error["loc"])
            diagnostics.append(
                DeploymentDiagnostic(diagnostic_id, "error", _path(location), summary)
            )
        return None, diagnostics
    reference_errors = _reference_diagnostics(resource)
    return (None, reference_errors) if reference_errors else (resource, [])


def _plan_reference(
    reference_id: str,
    field: str,
    value: str | None,
) -> DeploymentPlanReference | None:
    if value is None:
        return None
    return DeploymentPlanReference(reference_id, field, value)


def _selected_references(resource: FabricDeployment) -> tuple[DeploymentPlanReference, ...]:
    spec = resource.spec
    selected = [
        _plan_reference(
            "deployment.reference.tenant_identity",
            "spec.connection.tenantIdFrom",
            spec.connection.tenant_id_from,
        ),
        _plan_reference(
            "deployment.reference.workload_identity",
            "spec.connection.workloadIdentityRef",
            spec.connection.workload_identity_ref,
        ),
    ]
    if spec.controls is not None:
        selected.extend(
            [
                _plan_reference(
                    "deployment.reference.control_profile",
                    "spec.controls.profileRef",
                    spec.controls.profile_ref,
                ),
                _plan_reference(
                    "deployment.reference.policy",
                    "spec.controls.policyRef",
                    spec.controls.policy_ref,
                ),
                _plan_reference(
                    "deployment.reference.authorization",
                    "spec.controls.authorizationRef",
                    spec.controls.authorization_ref,
                ),
                _plan_reference(
                    "deployment.reference.pii",
                    "spec.controls.piiRef",
                    spec.controls.pii_ref,
                ),
                _plan_reference(
                    "deployment.reference.guardrail",
                    "spec.controls.guardrailRef",
                    spec.controls.guardrail_ref,
                ),
                _plan_reference(
                    "deployment.reference.escalation",
                    "spec.controls.escalationRef",
                    spec.controls.escalation_ref,
                ),
            ]
        )
    selected.append(
        _plan_reference(
            "deployment.reference.relay",
            "spec.observe.relayRef",
            spec.observe.relay_ref,
        )
    )
    if spec.assurance is not None:
        selected.append(
            _plan_reference(
                "deployment.reference.assurance_plan",
                "spec.assurance.planRef",
                spec.assurance.plan_ref,
            )
        )
    if spec.rollout is not None:
        selected.append(
            _plan_reference(
                "deployment.reference.rollout_approval",
                "spec.rollout.approvalRef",
                spec.rollout.approval_ref,
            )
        )
    return tuple(item for item in selected if item is not None)


def _selected_roles(resource: FabricDeployment) -> tuple[DeploymentPlanRole, ...]:
    spec = resource.spec
    integration_artifacts = {
        "sdk": "Fabric SDK",
        "adapter": "Fabric Adapter",
        "gateway": "Fabric Gateway",
        "otlp": "Fabric Collector OTLP receiver",
    }
    roles = [
        DeploymentPlanRole(
            "deployment.role.connect",
            "connect",
            integration_artifacts[spec.connection.mode],
            "Integrate the agent and propagate stable Fabric identity",
        )
    ]
    if spec.controls is not None:
        roles.append(
            DeploymentPlanRole(
                "deployment.role.control",
                "control",
                "Fabric Control integration",
                "Enforce only the referenced runtime control profile and bindings",
            )
        )
    roles.append(
        DeploymentPlanRole(
            "deployment.role.collector",
            "observe",
            "Fabric Collector",
            "Receive, normalize, privacy-process, correlate, and route telemetry",
        )
    )
    if spec.observe.relay_ref is not None:
        roles.append(
            DeploymentPlanRole(
                "deployment.role.relay",
                "observe",
                "Fabric Relay",
                "Durably export approved telemetry to the referenced destination",
            )
        )
    if spec.assurance is not None:
        roles.append(
            DeploymentPlanRole(
                "deployment.role.assurance_runner",
                "assurance",
                "Fabric Assurance Runner",
                "Run the referenced assurance plan outside the agent request path",
            )
        )
    return tuple(roles)


def _operator_prerequisites(
    resource: FabricDeployment,
) -> tuple[DeploymentPlanPrerequisite, ...]:
    spec = resource.spec
    prerequisites = [
        DeploymentPlanPrerequisite(
            "deployment.prerequisite.tenant_identity_authorized",
            "required",
            "Authorize the selected runtime to use the referenced tenant identity",
        ),
        DeploymentPlanPrerequisite(
            f"deployment.prerequisite.connection.{spec.connection.mode}",
            "required",
            "Install and configure the selected connection artifact at the agent boundary",
        ),
        DeploymentPlanPrerequisite(
            "deployment.prerequisite.collector_ready",
            "required",
            "Provide a reachable Fabric Collector with privacy and delivery health configured",
        ),
        DeploymentPlanPrerequisite(
            f"deployment.prerequisite.content_mode.{spec.observe.content_mode}",
            "required",
            "Confirm the selected Observe content mode is approved for this deployment",
        ),
    ]
    if spec.connection.workload_identity_ref is not None:
        prerequisites.append(
            DeploymentPlanPrerequisite(
                "deployment.prerequisite.workload_identity_bound",
                "required",
                "Bind and authorize the referenced workload identity independently of telemetry",
            )
        )
    if spec.controls is not None:
        prerequisites.append(
            DeploymentPlanPrerequisite(
                "deployment.prerequisite.control_profile_available",
                "required",
                "Make the referenced control profile available at the enforcement point",
            )
        )
        prerequisites.append(
            DeploymentPlanPrerequisite(
                "deployment.prerequisite.control_failure_posture_reviewed",
                "required",
                "Review timeout, bypass, fail-open, and fail-closed behavior for every control",
            )
        )
    if spec.observe.relay_ref is not None:
        prerequisites.append(
            DeploymentPlanPrerequisite(
                "deployment.prerequisite.relay_ready",
                "required",
                "Provision the referenced Relay with authenticated export and delivery monitoring",
            )
        )
    if spec.assurance is not None:
        prerequisites.append(
            DeploymentPlanPrerequisite(
                "deployment.prerequisite.assurance_plan_qualified",
                "required",
                "Run and review the referenced assurance plan before rollout",
            )
        )
    if spec.rollout is not None:
        prerequisites.append(
            DeploymentPlanPrerequisite(
                "deployment.prerequisite.rollout_approval_verified",
                "required",
                "Independently verify the referenced approval before any rollout",
            )
        )
    level_prerequisites = {
        "A0": (
            "deployment.prerequisite.a0.synthetic_data",
            "Keep development use limited to approved synthetic or non-sensitive data",
        ),
        "A1": (
            "deployment.prerequisite.a1.delivery_monitoring",
            "Establish operator ownership for authenticated export and delivery-loss alerts",
        ),
        "A2": (
            "deployment.prerequisite.a2.incident_readiness",
            "Document incident retention and recovery ownership for the controlled deployment",
        ),
        "A3": (
            "deployment.prerequisite.a3.separation_of_duties",
            "Establish separation of duties for policy, approval, operation, and investigation",
        ),
    }
    prerequisite_id, summary = level_prerequisites[spec.assurance_level]
    prerequisites.append(DeploymentPlanPrerequisite(prerequisite_id, "required", summary))
    if spec.assurance_level == "A3":
        prerequisites.append(
            DeploymentPlanPrerequisite(
                "deployment.prerequisite.a3.recovery_evidence",
                "required",
                "Test recovery and retain reconstruction evidence in a "
                "customer-approved destination",
            )
        )
    return tuple(prerequisites)


def build_deployment_plan(resource: FabricDeployment) -> DeploymentPlan:
    """Build a deterministic offline plan without resolving or applying anything."""

    integration_artifacts = {
        "sdk": "Fabric SDK",
        "adapter": "Fabric Adapter",
        "gateway": "Fabric Gateway",
        "otlp": "Fabric Collector OTLP receiver",
    }
    return DeploymentPlan(
        assurance_level=resource.spec.assurance_level,
        integration_mode=resource.spec.connection.mode,
        integration_artifact=integration_artifacts[resource.spec.connection.mode],
        roles=_selected_roles(resource),
        references=_selected_references(resource),
        prerequisites=_operator_prerequisites(resource),
    )


def deployment_plan_payload(plan: DeploymentPlan) -> dict[str, object]:
    """Convert a plan to its stable JSON-compatible representation."""

    return {
        "assurance_level": plan.assurance_level,
        "integration": {
            "mode": plan.integration_mode,
            "artifact": plan.integration_artifact,
        },
        "roles": [asdict(item) for item in plan.roles],
        "references": [asdict(item) for item in plan.references],
        "prerequisites": [asdict(item) for item in plan.prerequisites],
    }


def canonical_document(value: Any) -> bytes:
    """Return canonical UTF-8 JSON containing every declared input field."""

    return json.dumps(
        value,
        ensure_ascii=False,
        allow_nan=False,
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")


def deployment_digest(value: Any) -> str:
    """Return the SHA-256 identity of the complete canonical document."""

    return "sha256:" + hashlib.sha256(canonical_document(value)).hexdigest()


def diagnostic_payload(diagnostic: DeploymentDiagnostic) -> dict[str, str]:
    """Convert a diagnostic to its stable wire representation."""

    return asdict(diagnostic)
