# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Operational command line interface for the Fabric Python distribution.

``fabricctl`` is intentionally read-only-first.  Its initial commands inspect
configuration and exercise the local instrumentation path; none makes a
network request or changes host configuration.
"""

from __future__ import annotations

import argparse
import json
import os
import stat
import sys
from collections.abc import Mapping, Sequence
from dataclasses import asdict, dataclass
from importlib import metadata
from pathlib import Path
from typing import Literal, cast
from urllib.parse import SplitResult, urlsplit, urlunsplit

from opentelemetry.sdk.trace import ReadableSpan, TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

from ._version import __version__
from .client import (
    DEFAULT_PROFILE,
    ENV_AGENT,
    ENV_NEMO_SOCKET,
    ENV_PRESIDIO_SOCKET,
    ENV_PROFILE,
    ENV_TENANT,
    Fabric,
    FabricConfig,
)
from .deployment import (
    DeploymentDiagnostic,
    DeploymentDocumentError,
    FabricDeployment,
    build_deployment_plan,
    deployment_digest,
    deployment_plan_payload,
    diagnostic_payload,
    load_deployment,
    validate_deployment,
)

_DISTRIBUTION = "singleaxis-fabric"
_PROGRAM = "fabricctl"
_CAPTURE_CONTENT = "FABRIC_CAPTURE_LLM_CONTENT"
_OTLP_ENDPOINT = "OTEL_EXPORTER_OTLP_ENDPOINT"
_TRUE_VALUES = frozenset({"1", "true", "yes", "on"})
_FALSE_VALUES = frozenset({"", "0", "false", "no", "off"})

_EXIT_OK = 0
_EXIT_WARNING = 1
_EXIT_FAILED = 2

Status = Literal["pass", "warn", "fail"]


@dataclass(frozen=True)
class Check:
    """One deterministic diagnostic result."""

    id: str
    status: Status
    severity: Literal["info", "warning", "error"]
    summary: str
    detail: str


def _check(
    check_id: str,
    status: Status,
    summary: str,
    detail: str,
) -> Check:
    severity = {"pass": "info", "warn": "warning", "fail": "error"}[status]
    return Check(
        check_id,
        status,
        cast("Literal['info', 'warning', 'error']", severity),
        summary,
        detail,
    )


def _python_check() -> Check:
    current = sys.version_info[:3]
    rendered = ".".join(str(part) for part in current)
    if current < (3, 11):
        return _check("runtime.python", "fail", "Unsupported Python runtime", rendered)
    return _check("runtime.python", "pass", "Supported Python runtime", rendered)


def _package_check() -> Check:
    try:
        dist = metadata.distribution(_DISTRIBUTION)
    except metadata.PackageNotFoundError:
        return _check(
            "package.identity",
            "fail",
            "Fabric distribution metadata is unavailable",
            "Install the singleaxis-fabric distribution before running fabricctl.",
        )
    name = str(dist.metadata["Name"] or "")
    version = dist.version
    if name.lower() != _DISTRIBUTION or version != __version__:
        return _check(
            "package.identity",
            "fail",
            "Package identity mismatch",
            f"expected={_DISTRIBUTION}/{__version__}; found={name or 'unknown'}/{version}",
        )
    return _check(
        "package.identity", "pass", "Fabric package identity verified", f"{name} {version}"
    )


def _identifier_check(env: Mapping[str, str], variable: str, check_id: str) -> Check:
    value = env.get(variable, "")
    if not value.strip():
        return _check(check_id, "fail", f"{variable} is required", "not configured")
    try:
        if variable == ENV_TENANT:
            FabricConfig(tenant_id=value, agent_id="fabricctl-validation")
        else:
            FabricConfig(tenant_id="fabricctl-validation", agent_id=value)
    except (TypeError, ValueError) as exc:
        return _check(check_id, "fail", f"{variable} is invalid", str(exc))
    return _check(check_id, "pass", f"{variable} is configured", "valid identifier")


def _endpoint_check(env: Mapping[str, str]) -> Check:
    raw = env.get(_OTLP_ENDPOINT, "").strip()
    if not raw:
        return _check(
            "telemetry.otlp_endpoint",
            "warn",
            "OTLP export endpoint is not configured",
            "Local spans will not be delivered unless the host configures another exporter.",
        )
    parsed = urlsplit(raw)
    if (
        parsed.scheme not in {"http", "https"}
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
    ):
        return _check(
            "telemetry.otlp_endpoint",
            "fail",
            "OTLP export endpoint is invalid",
            "Use an http(s) URL with a hostname and no credentials, query, or fragment.",
        )
    try:
        port = parsed.port
    except ValueError:
        return _check(
            "telemetry.otlp_endpoint",
            "fail",
            "OTLP export endpoint is invalid",
            "The endpoint contains an invalid port.",
        )
    authority = parsed.hostname
    if port is not None:
        authority = f"{authority}:{port}"
    safe = urlunsplit(SplitResult(parsed.scheme, authority, parsed.path, "", ""))
    return _check("telemetry.otlp_endpoint", "pass", "OTLP export endpoint is valid", safe)


def _content_capture_check(env: Mapping[str, str]) -> Check:
    raw = env.get(_CAPTURE_CONTENT, "").strip().lower()
    if raw in _TRUE_VALUES:
        return _check(
            "privacy.llm_content_capture",
            "fail",
            "Raw LLM content capture is enabled",
            "Disable FABRIC_CAPTURE_LLM_CONTENT unless an approved data policy requires it.",
        )
    if raw not in _FALSE_VALUES:
        return _check(
            "privacy.llm_content_capture",
            "fail",
            "Raw LLM content capture setting is invalid",
            "Use true/false, yes/no, on/off, or 1/0.",
        )
    return _check(
        "privacy.llm_content_capture",
        "pass",
        "Raw LLM content capture is disabled",
        "privacy-preserving default",
    )


def _socket_check(env: Mapping[str, str], variable: str, check_id: str) -> Check:
    configured = env.get(variable, "").strip()
    if not configured:
        return _check(check_id, "pass", f"{variable} is not configured", "optional")
    path = Path(configured)
    try:
        mode = path.stat().st_mode
    except OSError as exc:
        return _check(check_id, "fail", f"{variable} is unavailable", exc.strerror or "not found")
    if not stat.S_ISSOCK(mode):
        return _check(
            check_id,
            "fail",
            f"{variable} is not a Unix-domain socket",
            "configured path exists but is not a socket",
        )
    return _check(check_id, "pass", f"{variable} socket is available", "socket exists")


def run_checks(env: Mapping[str, str] | None = None) -> list[Check]:
    """Run all read-only diagnostics in stable display order."""

    source = os.environ if env is None else env
    return [
        _python_check(),
        _package_check(),
        _identifier_check(source, ENV_TENANT, "identity.tenant_id"),
        _identifier_check(source, ENV_AGENT, "identity.agent_id"),
        _endpoint_check(source),
        _content_capture_check(source),
        _socket_check(source, ENV_PRESIDIO_SOCKET, "sidecar.presidio_socket"),
        _socket_check(source, ENV_NEMO_SOCKET, "sidecar.nemo_socket"),
    ]


def _exit_for(checks: Sequence[Check]) -> int:
    if any(item.status == "fail" for item in checks):
        return _EXIT_FAILED
    if any(item.status == "warn" for item in checks):
        return _EXIT_WARNING
    return _EXIT_OK


def _render_checks(checks: Sequence[Check], *, json_output: bool) -> None:
    exit_code = _exit_for(checks)
    if json_output:
        payload = {
            "schema_version": "fabricctl.diagnostics/v1",
            "status": {0: "pass", 1: "warn", 2: "fail"}[exit_code],
            "checks": [asdict(item) for item in checks],
        }
        print(json.dumps(payload, indent=2, sort_keys=True))
        return
    labels = {"pass": "PASS", "warn": "WARN", "fail": "FAIL"}
    for item in checks:
        print(f"[{labels[item.status]}] {item.id}: {item.summary}")
        print(f"       {item.detail}")
    result_label = {0: "PASS", 1: "WARN", 2: "FAIL"}[exit_code]
    print(f"\nResult: {result_label}")


def _doctor(args: argparse.Namespace) -> int:
    checks = run_checks()
    _render_checks(checks, json_output=args.json)
    return _exit_for(checks)


def _safe_endpoint(raw: str) -> str:
    """Render an endpoint without ever returning embedded credentials."""

    parsed = urlsplit(raw)
    if not parsed.hostname:
        return "<invalid>"
    authority = parsed.hostname
    try:
        if parsed.port is not None:
            authority = f"{authority}:{parsed.port}"
    except ValueError:
        return "<invalid>"
    return urlunsplit(SplitResult(parsed.scheme, authority, parsed.path, "", ""))


def _config_payload(env: Mapping[str, str]) -> dict[str, object]:
    capture_raw = env.get(_CAPTURE_CONTENT, "").strip().lower()
    endpoint = env.get(_OTLP_ENDPOINT, "").strip()
    return {
        "schema_version": "fabricctl.config/v1",
        "fabric": {
            "tenant_id": env.get(ENV_TENANT, "") or "<unset>",
            "agent_id": env.get(ENV_AGENT, "") or "<unset>",
            "profile": env.get(ENV_PROFILE, DEFAULT_PROFILE),
        },
        "telemetry": {"otlp_endpoint": _safe_endpoint(endpoint) if endpoint else "<unset>"},
        "privacy": {
            "llm_content_capture": capture_raw in _TRUE_VALUES,
        },
        "sidecars": {
            "presidio": "configured" if env.get(ENV_PRESIDIO_SOCKET, "").strip() else "disabled",
            "nemo": "configured" if env.get(ENV_NEMO_SOCKET, "").strip() else "disabled",
        },
    }


def _config_show(args: argparse.Namespace) -> int:
    payload = _config_payload(os.environ)
    if args.json:
        print(json.dumps(payload, indent=2, sort_keys=True))
    else:
        fabric = cast("dict[str, object]", payload["fabric"])
        telemetry = cast("dict[str, object]", payload["telemetry"])
        privacy = cast("dict[str, object]", payload["privacy"])
        sidecars = cast("dict[str, object]", payload["sidecars"])
        print(f"tenant_id: {fabric['tenant_id']}")
        print(f"agent_id: {fabric['agent_id']}")
        print(f"profile: {fabric['profile']}")
        print(f"otlp_endpoint: {telemetry['otlp_endpoint']}")
        print(f"llm_content_capture: {str(privacy['llm_content_capture']).lower()}")
        print(f"presidio: {sidecars['presidio']}")
        print(f"nemo: {sidecars['nemo']}")
    return _EXIT_OK


def _config_validate(args: argparse.Namespace) -> int:
    checks = run_checks()[2:]
    _render_checks(checks, json_output=args.json)
    return _exit_for(checks)


def _deployment_resource_identity(value: object) -> dict[str, str] | None:
    if not isinstance(value, dict):
        return None
    metadata_value = value.get("metadata")
    if not isinstance(metadata_value, dict):
        return None
    name_value = metadata_value.get("name")
    if not isinstance(name_value, str):
        return None
    return {
        "apiVersion": str(value.get("apiVersion", "")),
        "kind": str(value.get("kind", "")),
        "name": name_value,
    }


def _load_validated_deployment(
    path: Path,
) -> tuple[object | None, FabricDeployment | None, list[DeploymentDiagnostic]]:
    try:
        value = load_deployment(path)
    except DeploymentDocumentError as exc:
        return None, None, [exc.diagnostic]
    resource, diagnostics = validate_deployment(value)
    return value, resource, diagnostics


def _render_deployment_validation(
    diagnostics: Sequence[DeploymentDiagnostic],
    *,
    json_output: bool,
) -> None:
    if json_output:
        print(
            json.dumps(
                {
                    "schema_version": "fabricctl.deployment-validation/v1",
                    "status": "fail" if diagnostics else "pass",
                    "diagnostics": [diagnostic_payload(item) for item in diagnostics],
                },
                indent=2,
                sort_keys=True,
            )
        )
        return
    if not diagnostics:
        print("[PASS] FabricDeployment is valid")
        return
    for item in diagnostics:
        print(f"[FAIL] {item.id} at {item.path}: {item.summary}")
    print("\nResult: FAIL")


def _deployment_validate(args: argparse.Namespace) -> int:
    _value, _resource, diagnostics = _load_validated_deployment(Path(args.file))
    _render_deployment_validation(diagnostics, json_output=args.json)
    return _EXIT_FAILED if diagnostics else _EXIT_OK


def _deployment_digest(args: argparse.Namespace) -> int:
    value, _resource, diagnostics = _load_validated_deployment(Path(args.file))
    if diagnostics or value is None:
        _render_deployment_validation(diagnostics, json_output=args.json)
        return _EXIT_FAILED
    digest = deployment_digest(value)
    if args.json:
        print(
            json.dumps(
                {
                    "schema_version": "fabricctl.deployment-digest/v1",
                    "algorithm": "sha256",
                    "digest": digest,
                    "resource": _deployment_resource_identity(value),
                },
                indent=2,
                sort_keys=True,
            )
        )
    else:
        print(digest)
    return _EXIT_OK


def _deployment_plan(args: argparse.Namespace) -> int:
    value, resource, diagnostics = _load_validated_deployment(Path(args.file))
    if diagnostics or value is None or resource is None:
        _render_deployment_validation(diagnostics, json_output=args.json)
        return _EXIT_FAILED
    plan = build_deployment_plan(resource)
    payload = {
        "schema_version": "fabricctl.deployment-plan/v1",
        "status": "pass",
        "operation": {"mode": "offline", "mutating": False},
        "resource": {
            **(_deployment_resource_identity(value) or {}),
            "digest": deployment_digest(value),
        },
        **deployment_plan_payload(plan),
    }
    if args.json:
        print(json.dumps(payload, indent=2, sort_keys=True))
        return _EXIT_OK

    resource_identity = cast("dict[str, object]", payload["resource"])
    integration = cast("dict[str, object]", payload["integration"])
    print(f"FabricDeployment plan: {resource_identity['name']}")
    print(f"Digest: {resource_identity['digest']}")
    print(f"Assurance level: {payload['assurance_level']}")
    print(f"Integration: {integration['mode']} ({integration['artifact']})")
    print("\nRequired OSS roles:")
    for role in plan.roles:
        print(f"- {role.id}: {role.artifact} [{role.plane}]")
        print(f"  {role.purpose}")
    print("\nOpaque references (not resolved):")
    for reference in plan.references:
        print(f"- {reference.id}: {reference.field} -> {reference.reference}")
    print("\nOperator prerequisites (not verified):")
    for prerequisite in plan.prerequisites:
        print(f"- [REQUIRED] {prerequisite.id}: {prerequisite.summary}")
    print("\nNo changes were applied. No network, cluster, or platform was contacted.")
    return _EXIT_OK


def _verify_correlation(spans: Sequence[ReadableSpan]) -> list[str]:
    """Return deterministic violations found in the synthetic trace."""

    expected_span_count = 3
    by_name = {span.name: span for span in spans}
    decision_span = by_name.get("fabric.decision")
    llm_span = by_name.get("chat fabricctl-model")
    tool_span = by_name.get("fabricctl.echo")
    errors: list[str] = []
    if (
        len(spans) != expected_span_count
        or decision_span is None
        or llm_span is None
        or tool_span is None
    ):
        errors.append("expected exactly one decision, model, and tool span")
    if decision_span is None:
        return errors

    attrs = dict(decision_span.attributes or {})
    required = {
        "fabric.tenant_id": "fabricctl-local",
        "fabric.agent_id": "synthetic-agent",
        "fabric.session_id": "fabricctl-session",
        "fabric.request_id": "fabricctl-request",
        "fabric.decision_id": "fabricctl-decision",
    }
    for key, expected in required.items():
        if attrs.get(key) != expected:
            errors.append(f"decision span missing {key}")
    for child_name, child in (("model", llm_span), ("tool", tool_span)):
        if child is None:
            continue
        if child.parent is None:
            errors.append(f"{child_name} span has no parent")
        elif child.parent.span_id != decision_span.context.span_id:
            errors.append(f"{child_name} span is not parented to decision span")
    return errors


def _verify_local(args: argparse.Namespace) -> int:
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    tracer = provider.get_tracer("fabricctl-local-verifier", __version__)
    config = FabricConfig(
        tenant_id="fabricctl-local",
        agent_id="synthetic-agent",
        profile=DEFAULT_PROFILE,
    )
    client = Fabric(config, tracer=tracer)
    try:
        with client.decision(
            session_id="fabricctl-session",
            request_id="fabricctl-request",
            decision_id="fabricctl-decision",
        ) as decision:
            with decision.llm_call(provider="synthetic", model="fabricctl-model") as call:
                call.set_usage(input_tokens=1, output_tokens=1, finish_reason="stop")
            with decision.tool_call(name="fabricctl.echo", call_id="fabricctl-tool") as tool:
                tool.set_result_count(1)
    finally:
        client.close()
        provider.force_flush()
        provider.shutdown()

    errors = _verify_correlation(exporter.get_finished_spans())

    status = "fail" if errors else "pass"
    result = {
        "schema_version": "fabricctl.verify/v1",
        "mode": "local",
        "status": status,
        "spans": {"decision": 1, "model": 1, "tool": 1} if not errors else {},
        "correlation": "verified" if not errors else "failed",
        "errors": errors,
    }
    if args.json:
        print(json.dumps(result, indent=2, sort_keys=True))
    elif errors:
        print("[FAIL] Local trace verification failed")
        for error in errors:
            print(f"       {error}")
    else:
        print("[PASS] Local trace verification")
        print("       decision=1 model=1 tool=1 correlation=verified")
        print("       No network request was made.")
    return _EXIT_FAILED if errors else _EXIT_OK


def _version(_args: argparse.Namespace) -> int:
    print(f"{_PROGRAM} {__version__}")
    return _EXIT_OK


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog=_PROGRAM,
        description="Inspect and verify a SingleAxis Fabric installation.",
    )
    parser.add_argument("--version", action="version", version=f"%(prog)s {__version__}")
    subparsers = parser.add_subparsers(dest="command", required=True)

    version_parser = subparsers.add_parser("version", help="Print the installed version.")
    version_parser.set_defaults(handler=_version)

    doctor_parser = subparsers.add_parser("doctor", help="Run read-only environment diagnostics.")
    doctor_parser.add_argument("--json", action="store_true", help="Emit stable JSON output.")
    doctor_parser.set_defaults(handler=_doctor)

    verify_parser = subparsers.add_parser(
        "verify", help="Verify Fabric behavior without exporting data."
    )
    verify_parser.add_argument(
        "--local",
        action="store_true",
        required=True,
        help="Emit and validate a synthetic trace in memory; no network is used.",
    )
    verify_parser.add_argument("--json", action="store_true", help="Emit stable JSON output.")
    verify_parser.set_defaults(handler=_verify_local)

    config_parser = subparsers.add_parser(
        "config", help="Inspect effective environment configuration."
    )
    config_subparsers = config_parser.add_subparsers(dest="config_command", required=True)
    show_parser = config_subparsers.add_parser("show", help="Show safe effective configuration.")
    show_parser.add_argument("--json", action="store_true", help="Emit stable JSON output.")
    show_parser.set_defaults(handler=_config_show)
    validate_parser = config_subparsers.add_parser(
        "validate", help="Validate effective configuration."
    )
    validate_parser.add_argument("--json", action="store_true", help="Emit stable JSON output.")
    validate_parser.set_defaults(handler=_config_validate)

    deployment_parser = subparsers.add_parser(
        "deployment",
        help="Validate or identify a declarative FabricDeployment file locally.",
    )
    deployment_subparsers = deployment_parser.add_subparsers(
        dest="deployment_command", required=True
    )
    deployment_validate_parser = deployment_subparsers.add_parser(
        "validate",
        help="Fail-closed validation of a v1alpha1 deployment file; no network is used.",
    )
    deployment_validate_parser.add_argument("file", help="YAML or JSON deployment file.")
    deployment_validate_parser.add_argument(
        "--json", action="store_true", help="Emit stable JSON output."
    )
    deployment_validate_parser.set_defaults(handler=_deployment_validate)
    deployment_digest_parser = deployment_subparsers.add_parser(
        "digest",
        help="Validate and SHA-256 digest the complete canonical deployment document.",
    )
    deployment_digest_parser.add_argument("file", help="YAML or JSON deployment file.")
    deployment_digest_parser.add_argument(
        "--json", action="store_true", help="Emit stable JSON output."
    )
    deployment_digest_parser.set_defaults(handler=_deployment_digest)
    deployment_plan_parser = deployment_subparsers.add_parser(
        "plan",
        help="Create an offline, non-mutating installation and readiness plan.",
    )
    deployment_plan_parser.add_argument("file", help="YAML or JSON deployment file.")
    deployment_plan_parser.add_argument(
        "--json", action="store_true", help="Emit stable JSON output."
    )
    deployment_plan_parser.set_defaults(handler=_deployment_plan)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    """Run ``fabricctl`` and return a process-compatible exit code."""

    parser = build_parser()
    args = parser.parse_args(argv)
    return int(args.handler(args))


if __name__ == "__main__":  # pragma: no cover - console entry point owns this path
    raise SystemExit(main())
