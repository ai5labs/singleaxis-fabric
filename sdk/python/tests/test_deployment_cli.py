# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Contract and CLI qualification for FabricDeployment v1alpha1."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

import jsonschema
import pytest
import yaml

from fabric import cli
from fabric.deployment import (
    MAX_DOCUMENT_BYTES,
    DeploymentDocumentError,
    canonical_document,
    deployment_digest,
    load_deployment,
    validate_deployment,
)

_ROOT = Path(__file__).resolve().parents[3]
_CONTRACT = _ROOT / "contracts" / "management" / "v1alpha1"


def _load_fixture(path: Path) -> object:
    if path.suffix == ".json":
        return json.loads(path.read_text(encoding="utf-8"))
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def _deployment_for_level(level: str) -> dict[str, object]:
    modes = {"A0": "sdk", "A1": "adapter", "A2": "gateway", "A3": "otlp"}
    spec: dict[str, object] = {
        "assuranceLevel": level,
        "connection": {
            "mode": modes[level],
            "tenantIdFrom": f"tenant-{level.lower()}",
        },
        "observe": {"contentMode": "hash-only"},
    }
    if level in {"A1", "A2", "A3"}:
        observe = spec["observe"]
        assert isinstance(observe, dict)
        observe["relayRef"] = f"relay-{level.lower()}"
    if level == "A3":
        connection = spec["connection"]
        assert isinstance(connection, dict)
        connection["workloadIdentityRef"] = "regulated-workload-identity"
    if level in {"A2", "A3"}:
        spec["controls"] = {"profileRef": f"controls-{level.lower()}"}
        spec["assurance"] = {"planRef": f"assurance-{level.lower()}"}
        spec["rollout"] = {"approvalRef": f"approval-{level.lower()}"}
    return {
        "apiVersion": "fabric.singleaxis.dev/v1alpha1",
        "kind": "FabricDeployment",
        "metadata": {"name": f"agent-{level.lower()}"},
        "spec": spec,
    }


def test_contract_manifest_pins_every_declared_file() -> None:
    manifest = json.loads((_CONTRACT / "manifest.json").read_text(encoding="utf-8"))
    assert manifest["contract"] == "singleaxis.fabric.management.FabricDeployment"
    assert manifest["version"] == "v1alpha1"
    paths = {entry["path"] for entry in manifest["files"]}
    expected = {
        path.relative_to(_CONTRACT).as_posix()
        for path in _CONTRACT.rglob("*")
        if path.is_file() and path.name != "manifest.json"
    }
    assert paths == expected
    for entry in manifest["files"]:
        content = (_CONTRACT / entry["path"]).read_bytes()
        assert hashlib.sha256(content).hexdigest() == entry["sha256"]


@pytest.mark.parametrize("path", sorted((_CONTRACT / "valid").iterdir()))
def test_valid_fixtures_match_schema_and_runtime(path: Path) -> None:
    schema = json.loads(
        (_CONTRACT / "schema" / "fabric-deployment.schema.json").read_text(encoding="utf-8")
    )
    value = _load_fixture(path)
    jsonschema.Draft202012Validator(schema).validate(value)
    resource, diagnostics = validate_deployment(value)
    assert resource is not None
    assert diagnostics == []


@pytest.mark.parametrize("path", sorted((_CONTRACT / "invalid").iterdir()))
def test_invalid_fixtures_fail_schema_and_runtime(path: Path) -> None:
    schema = json.loads(
        (_CONTRACT / "schema" / "fabric-deployment.schema.json").read_text(encoding="utf-8")
    )
    value = _load_fixture(path)
    assert list(jsonschema.Draft202012Validator(schema).iter_errors(value))
    resource, diagnostics = validate_deployment(value)
    assert resource is None
    assert diagnostics


@pytest.mark.parametrize(
    ("schema_name", "fixture_root"),
    [
        ("fabric-install-target.schema.json", _CONTRACT / "install-target"),
        (
            "fabric-secret-requirements.schema.json",
            _CONTRACT / "derived" / "secret-requirements",
        ),
        (
            "fabric-installation-plan.schema.json",
            _CONTRACT / "derived" / "installation-plan",
        ),
        (
            "fabric-bundle-manifest.schema.json",
            _CONTRACT / "derived" / "bundle-manifest",
        ),
        (
            "fabric-bundle-build-result.schema.json",
            _CONTRACT / "derived" / "bundle-build-result",
        ),
        (
            "fabric-bundle-verification-report.schema.json",
            _CONTRACT / "derived" / "bundle-verification-report",
        ),
    ],
)
def test_offline_bundle_contract_fixtures(
    schema_name: str,
    fixture_root: Path,
) -> None:
    schema = json.loads((_CONTRACT / "schema" / schema_name).read_text(encoding="utf-8"))
    validator = jsonschema.Draft202012Validator(schema)
    for path in sorted((fixture_root / "valid").iterdir()):
        assert not list(validator.iter_errors(_load_fixture(path))), path
    for path in sorted((fixture_root / "invalid").iterdir()):
        assert list(validator.iter_errors(_load_fixture(path))), path


def test_validate_json_passes_offline_with_stable_envelope(
    capsys: pytest.CaptureFixture[str],
) -> None:
    path = _CONTRACT / "valid" / "a3-regulated.json"
    assert cli.main(["deployment", "validate", str(path), "--json"]) == 0
    assert json.loads(capsys.readouterr().out) == {
        "schema_version": "fabricctl.deployment-validation/v1",
        "status": "pass",
        "diagnostics": [],
    }


def test_validate_inline_secret_fails_without_echoing_value(
    capsys: pytest.CaptureFixture[str],
) -> None:
    path = _CONTRACT / "invalid" / "inline-secret.yaml"
    assert cli.main(["deployment", "validate", str(path), "--json"]) == 2
    output = capsys.readouterr().out
    payload = json.loads(output)
    assert payload["status"] == "fail"
    assert payload["diagnostics"][0]["id"] == "deployment.security.inline_sensitive_value"
    assert "do-not-put-secrets-here" not in output


def test_digest_is_deterministic_across_yaml_json_and_key_order(tmp_path: Path) -> None:
    value = _load_fixture(_CONTRACT / "valid" / "a3-regulated.json")
    json_path = tmp_path / "deployment.json"
    yaml_path = tmp_path / "deployment.yaml"
    json_path.write_text(json.dumps(value, sort_keys=False), encoding="utf-8")
    yaml_path.write_text(yaml.safe_dump(value, sort_keys=True), encoding="utf-8")
    loaded_json = load_deployment(json_path)
    loaded_yaml = load_deployment(yaml_path)
    assert canonical_document(loaded_json) == canonical_document(loaded_yaml)
    assert deployment_digest(loaded_json) == deployment_digest(loaded_yaml)


def test_digest_changes_when_any_declared_field_changes() -> None:
    original = _load_fixture(_CONTRACT / "valid" / "a3-regulated.json")
    assert isinstance(original, dict)
    changed = json.loads(json.dumps(original))
    changed["spec"]["rollout"]["approvalRef"] = "change-1843"
    assert deployment_digest(original) != deployment_digest(changed)


def test_digest_command_validates_first_and_returns_resource_identity(
    capsys: pytest.CaptureFixture[str],
) -> None:
    valid = _CONTRACT / "valid" / "a3-regulated.json"
    assert cli.main(["deployment", "digest", str(valid), "--json"]) == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["algorithm"] == "sha256"
    assert payload["digest"].startswith("sha256:")
    assert payload["resource"] == {
        "apiVersion": "fabric.singleaxis.dev/v1alpha1",
        "kind": "FabricDeployment",
        "name": "payments-agent-prod",
    }

    invalid = _CONTRACT / "invalid" / "raw-content.yaml"
    assert cli.main(["deployment", "digest", str(invalid), "--json"]) == 2
    assert json.loads(capsys.readouterr().out)["schema_version"] == (
        "fabricctl.deployment-validation/v1"
    )


@pytest.mark.parametrize(
    ("suffix", "content"),
    [
        (".yaml", "apiVersion: one\napiVersion: two\n"),
        (".json", '{"apiVersion":"one","apiVersion":"two"}'),
    ],
)
def test_duplicate_keys_are_rejected(suffix: str, content: str, tmp_path: Path) -> None:
    path = tmp_path / f"duplicate{suffix}"
    path.write_text(content, encoding="utf-8")
    with pytest.raises(DeploymentDocumentError) as caught:
        load_deployment(path)
    assert caught.value.diagnostic.id == "deployment.document.syntax"


def test_yaml_anchors_and_aliases_are_rejected_without_expansion(tmp_path: Path) -> None:
    path = tmp_path / "alias.yaml"
    path.write_text(
        "apiVersion: &version fabric.singleaxis.dev/v1alpha1\n"
        "kind: FabricDeployment\n"
        "metadata:\n"
        "  name: alias-agent\n"
        "spec:\n"
        "  assuranceLevel: A0\n"
        "  connection:\n"
        "    mode: sdk\n"
        "    tenantIdFrom: *version\n"
        "  observe:\n"
        "    contentMode: metadata-only\n",
        encoding="utf-8",
    )
    with pytest.raises(DeploymentDocumentError) as caught:
        load_deployment(path)
    diagnostic = caught.value.diagnostic
    assert diagnostic.id == "deployment.document.alias_forbidden"
    assert diagnostic.path == "$"
    assert "fabric.singleaxis.dev" not in diagnostic.summary


def test_file_errors_are_stable_and_value_free(tmp_path: Path) -> None:
    missing = tmp_path / "customer-secret-name.yaml"
    with pytest.raises(DeploymentDocumentError) as caught:
        load_deployment(missing)
    assert caught.value.diagnostic.id == "deployment.file.not_found"
    assert "customer-secret-name" not in caught.value.diagnostic.summary

    oversized = tmp_path / "oversized.yaml"
    oversized.write_bytes(b"x" * (MAX_DOCUMENT_BYTES + 1))
    with pytest.raises(DeploymentDocumentError) as caught_oversized:
        load_deployment(oversized)
    assert caught_oversized.value.diagnostic.id == "deployment.file.too_large"


def test_wrong_document_type_and_invalid_references_fail_closed() -> None:
    resource, diagnostics = validate_deployment(["not", "a", "mapping"])
    assert resource is None
    assert diagnostics[0].id == "deployment.document.type"

    value = _load_fixture(_CONTRACT / "valid" / "a0-local.yaml")
    assert isinstance(value, dict)
    value["metadata"]["name"] = "INVALID NAME"
    value["spec"]["connection"]["tenantIdFrom"] = "bad reference?"
    resource, diagnostics = validate_deployment(value)
    assert resource is None
    assert {item.id for item in diagnostics} == {
        "deployment.identity.invalid_name",
        "deployment.reference.invalid",
    }


@pytest.mark.parametrize(
    "credential_like_reference",
    [
        "AKIAIOSFODNN7EXAMPLE",
        "ghp_abcdefghijklmnopqrstuvwxyz123456",
        "0123456789abcdef0123456789abcdef01234567",
    ],
)
def test_credential_like_references_fail_without_echoing_value(
    credential_like_reference: str,
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    value = _load_fixture(_CONTRACT / "valid" / "a0-local.yaml")
    assert isinstance(value, dict)
    value["spec"]["connection"]["tenantIdFrom"] = credential_like_reference
    path = tmp_path / "deployment.yaml"
    path.write_text(yaml.safe_dump(value), encoding="utf-8")

    assert cli.main(["deployment", "validate", str(path), "--json"]) == 2
    output = capsys.readouterr().out
    payload = json.loads(output)
    assert payload["diagnostics"][0]["id"] == "deployment.reference.sensitive"
    assert credential_like_reference not in output


def test_cli_help_discloses_deployment_but_no_apply_surface(
    capsys: pytest.CaptureFixture[str],
) -> None:
    with pytest.raises(SystemExit) as caught:
        cli.main(["deployment", "--help"])
    assert caught.value.code == 0
    output = capsys.readouterr().out
    assert "validate" in output
    assert "digest" in output
    assert "plan" in output
    assert "apply" not in output


@pytest.mark.parametrize(
    ("level", "artifact", "role_ids", "reference_ids", "level_prerequisites"),
    [
        (
            "A0",
            "Fabric SDK",
            ["deployment.role.connect", "deployment.role.collector"],
            ["deployment.reference.tenant_identity"],
            ["deployment.prerequisite.a0.synthetic_data"],
        ),
        (
            "A1",
            "Fabric Adapter",
            [
                "deployment.role.connect",
                "deployment.role.collector",
                "deployment.role.relay",
            ],
            ["deployment.reference.tenant_identity", "deployment.reference.relay"],
            ["deployment.prerequisite.a1.delivery_monitoring"],
        ),
        (
            "A2",
            "Fabric Gateway",
            [
                "deployment.role.connect",
                "deployment.role.control",
                "deployment.role.collector",
                "deployment.role.relay",
                "deployment.role.assurance_runner",
            ],
            [
                "deployment.reference.tenant_identity",
                "deployment.reference.control_profile",
                "deployment.reference.relay",
                "deployment.reference.assurance_plan",
                "deployment.reference.rollout_approval",
            ],
            ["deployment.prerequisite.a2.incident_readiness"],
        ),
        (
            "A3",
            "Fabric Collector OTLP receiver",
            [
                "deployment.role.connect",
                "deployment.role.control",
                "deployment.role.collector",
                "deployment.role.relay",
                "deployment.role.assurance_runner",
            ],
            [
                "deployment.reference.tenant_identity",
                "deployment.reference.workload_identity",
                "deployment.reference.control_profile",
                "deployment.reference.relay",
                "deployment.reference.assurance_plan",
                "deployment.reference.rollout_approval",
            ],
            [
                "deployment.prerequisite.a3.separation_of_duties",
                "deployment.prerequisite.a3.recovery_evidence",
            ],
        ),
    ],
)
def test_plan_is_deterministic_and_level_selected(
    level: str,
    artifact: str,
    role_ids: list[str],
    reference_ids: list[str],
    level_prerequisites: list[str],
    tmp_path: Path,
    capsys: pytest.CaptureFixture[str],
) -> None:
    path = tmp_path / f"{level.lower()}.json"
    path.write_text(json.dumps(_deployment_for_level(level)), encoding="utf-8")

    assert cli.main(["deployment", "plan", str(path), "--json"]) == 0
    first = capsys.readouterr().out
    assert cli.main(["deployment", "plan", str(path), "--json"]) == 0
    second = capsys.readouterr().out
    assert first == second

    payload = json.loads(first)
    assert payload["schema_version"] == "fabricctl.deployment-plan/v1"
    assert payload["status"] == "pass"
    assert payload["operation"] == {"mode": "offline", "mutating": False}
    assert payload["assurance_level"] == level
    assert payload["integration"]["artifact"] == artifact
    assert [role["id"] for role in payload["roles"]] == role_ids
    assert [reference["id"] for reference in payload["references"]] == reference_ids
    prerequisite_ids = [item["id"] for item in payload["prerequisites"]]
    assert all(item in prerequisite_ids for item in level_prerequisites)
    serialized = json.dumps(payload)
    for unselected in ("SingleAxis Platform", "Governance", "Site Controller", "apply"):
        assert unselected not in serialized


def test_plan_reports_all_selected_reference_types_without_resolution(
    capsys: pytest.CaptureFixture[str],
) -> None:
    path = _CONTRACT / "valid" / "a3-regulated.json"
    assert cli.main(["deployment", "plan", str(path), "--json"]) == 0
    payload = json.loads(capsys.readouterr().out)
    references = {item["field"]: item["reference"] for item in payload["references"]}
    assert references == {
        "spec.connection.tenantIdFrom": "tenant-identity",
        "spec.connection.workloadIdentityRef": "payments-spiffe-binding-v2",
        "spec.controls.profileRef": "payments-agent-controls-v4",
        "spec.controls.policyRef": "payments-policy-v12",
        "spec.controls.authorizationRef": "payments-tool-authorization-v7",
        "spec.controls.piiRef": "payments-pii-v3",
        "spec.controls.guardrailRef": "payments-guardrails-v5",
        "spec.controls.escalationRef": "payments-escalation-v2",
        "spec.observe.relayRef": "regulated-relay-eu-west",
        "spec.assurance.planRef": "payments-release-gate-v7",
        "spec.rollout.approvalRef": "change-1842",
    }
    assert all(item["status"] == "required" for item in payload["prerequisites"])


def test_a3_requires_workload_identity_in_schema_and_runtime() -> None:
    path = _CONTRACT / "invalid" / "missing-a3-workload-identity.yaml"
    value = _load_fixture(path)
    schema = json.loads(
        (_CONTRACT / "schema" / "fabric-deployment.schema.json").read_text(encoding="utf-8")
    )
    schema_errors = list(jsonschema.Draft202012Validator(schema).iter_errors(value))
    assert schema_errors
    resource, diagnostics = validate_deployment(value)
    assert resource is None
    assert [item.id for item in diagnostics] == ["deployment.assurance.requirements"]
    assert all("regulated-workload-identity" not in item.summary for item in diagnostics)


def test_plan_invalid_input_reuses_safe_validation_diagnostics(
    capsys: pytest.CaptureFixture[str],
) -> None:
    path = _CONTRACT / "invalid" / "inline-secret.yaml"
    assert cli.main(["deployment", "validate", str(path), "--json"]) == 2
    validation = json.loads(capsys.readouterr().out)
    assert cli.main(["deployment", "plan", str(path), "--json"]) == 2
    plan_failure = json.loads(capsys.readouterr().out)
    assert plan_failure == validation
    assert "do-not-put-secrets-here" not in json.dumps(plan_failure)


def test_human_plan_is_explicitly_non_mutating(capsys: pytest.CaptureFixture[str]) -> None:
    path = _CONTRACT / "valid" / "a0-local.yaml"
    assert cli.main(["deployment", "plan", str(path)]) == 0
    output = capsys.readouterr().out
    assert "Required OSS roles:" in output
    assert "Operator prerequisites (not verified):" in output
    assert "No changes were applied" in output
    assert "No network, cluster, or platform was contacted" in output
