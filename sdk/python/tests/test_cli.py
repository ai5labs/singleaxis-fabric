# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Tests for the production-facing ``fabricctl`` command surface."""

from __future__ import annotations

import json
import os
import socket
import tempfile
from pathlib import Path
from types import SimpleNamespace

import pytest

from fabric import __version__, cli


def _healthy_env(**overrides: str) -> dict[str, str]:
    env = {
        "FABRIC_TENANT_ID": "acme",
        "FABRIC_AGENT_ID": "support-agent",
        "OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example.com/v1/traces",
    }
    env.update(overrides)
    return env


def test_version_command(capsys: pytest.CaptureFixture[str]) -> None:
    assert cli.main(["version"]) == 0
    assert capsys.readouterr().out == f"fabricctl {__version__}\n"


def test_global_version_flag(capsys: pytest.CaptureFixture[str]) -> None:
    with pytest.raises(SystemExit) as caught:
        cli.main(["--version"])
    assert caught.value.code == 0
    assert capsys.readouterr().out == f"fabricctl {__version__}\n"


def test_help_is_available(capsys: pytest.CaptureFixture[str]) -> None:
    with pytest.raises(SystemExit) as caught:
        cli.main(["--help"])
    assert caught.value.code == 0
    output = capsys.readouterr().out
    assert "doctor" in output
    assert "verify" in output
    assert "config" in output


def test_doctor_json_is_stable_and_healthy(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    original = cli.run_checks
    monkeypatch.setattr(cli, "run_checks", lambda: original(_healthy_env()))
    assert cli.main(["doctor", "--json"]) == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["schema_version"] == "fabricctl.diagnostics/v1"
    assert payload["status"] == "pass"
    assert [check["id"] for check in payload["checks"]] == [
        "runtime.python",
        "package.identity",
        "identity.tenant_id",
        "identity.agent_id",
        "telemetry.otlp_endpoint",
        "privacy.llm_content_capture",
        "sidecar.presidio_socket",
        "sidecar.nemo_socket",
    ]


def test_doctor_missing_ids_and_endpoint_has_meaningful_exit(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    original = cli.run_checks
    monkeypatch.setattr(cli, "run_checks", lambda: original({}))
    assert cli.main(["doctor"]) == 2
    output = capsys.readouterr().out
    assert "[FAIL] identity.tenant_id" in output
    assert "[FAIL] identity.agent_id" in output
    assert "[WARN] telemetry.otlp_endpoint" in output
    assert output.endswith("Result: FAIL\n")


@pytest.mark.parametrize(
    "endpoint",
    [
        "collector.example.com",
        "ftp://collector.example.com",
        "https://user:password@collector.example.com",
        "https://collector.example.com/traces?token=secret",
        "https://collector.example.com/traces#fragment",
        "https://collector.example.com:invalid",
    ],
)
def test_otlp_endpoint_rejects_unsafe_or_invalid_urls(endpoint: str) -> None:
    check = cli.run_checks(_healthy_env(OTEL_EXPORTER_OTLP_ENDPOINT=endpoint))[4]
    assert check.id == "telemetry.otlp_endpoint"
    assert check.status == "fail"
    assert "password" not in check.detail
    assert "secret" not in check.detail


@pytest.mark.parametrize("value", ["true", "TRUE", "1", "yes", "on"])
def test_content_capture_is_a_failure_for_regulated_default(value: str) -> None:
    check = cli.run_checks(_healthy_env(FABRIC_CAPTURE_LLM_CONTENT=value))[5]
    assert check.id == "privacy.llm_content_capture"
    assert check.status == "fail"


def test_configured_missing_socket_fails_without_disclosing_path(tmp_path: Path) -> None:
    sensitive_path = tmp_path / "customer-secret" / "presidio.sock"
    check = cli.run_checks(_healthy_env(FABRIC_PRESIDIO_UNIX_SOCKET=str(sensitive_path)))[6]
    assert check.status == "fail"
    assert str(sensitive_path) not in check.detail


def test_configured_regular_file_is_not_accepted_as_socket(tmp_path: Path) -> None:
    regular_file = tmp_path / "not-a-socket"
    regular_file.touch()
    check = cli.run_checks(_healthy_env(FABRIC_NEMO_UNIX_SOCKET=str(regular_file)))[7]
    assert check.status == "fail"
    assert "not a Unix-domain socket" in check.summary


def test_real_unix_socket_is_accepted() -> None:
    with tempfile.TemporaryDirectory(prefix="fctl-", dir="/tmp") as directory:
        path = Path(directory) / f"{os.getpid()}.sock"
        server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            server.bind(str(path))
            check = cli.run_checks(_healthy_env(FABRIC_PRESIDIO_UNIX_SOCKET=str(path)))[6]
        finally:
            server.close()
    assert check.status == "pass"


def test_config_show_allowlists_and_redacts_endpoint_credentials(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    env = _healthy_env(
        OTEL_EXPORTER_OTLP_ENDPOINT=(
            "https://fabric-user:super-secret@collector.example.com/v1/traces?token=hunter2"
        ),
        FABRIC_PRESIDIO_UNIX_SOCKET="/private/customer/presidio.sock",
        UNRELATED_API_KEY="do-not-print-this",
    )
    monkeypatch.setattr("fabric.cli.os.environ", env)
    assert cli.main(["config", "show", "--json"]) == 0
    output = capsys.readouterr().out
    payload = json.loads(output)
    assert payload["telemetry"]["otlp_endpoint"] == "https://collector.example.com/v1/traces"
    assert payload["sidecars"]["presidio"] == "configured"
    for secret in ("super-secret", "hunter2", "/private/customer", "do-not-print-this"):
        assert secret not in output


def test_config_validate_excludes_runtime_and_package_checks(
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    original = cli.run_checks
    monkeypatch.setattr(cli, "run_checks", lambda: original(_healthy_env()))
    assert cli.main(["config", "validate", "--json"]) == 0
    payload = json.loads(capsys.readouterr().out)
    ids = [check["id"] for check in payload["checks"]]
    assert "runtime.python" not in ids
    assert "package.identity" not in ids


def test_local_verify_emits_and_validates_three_correlated_spans(
    capsys: pytest.CaptureFixture[str],
) -> None:
    assert cli.main(["verify", "--local", "--json"]) == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload == {
        "correlation": "verified",
        "errors": [],
        "mode": "local",
        "schema_version": "fabricctl.verify/v1",
        "spans": {"decision": 1, "model": 1, "tool": 1},
        "status": "pass",
    }


def test_package_identity_mismatch_is_reported(monkeypatch: pytest.MonkeyPatch) -> None:
    fake = SimpleNamespace(
        metadata={"Name": "lookalike-fabric"},
        version=__version__,
    )
    monkeypatch.setattr("fabric.cli.metadata.distribution", lambda _name: fake)
    check = cli.run_checks(_healthy_env())[1]
    assert check.status == "fail"
    assert check.id == "package.identity"


def test_package_identity_accepts_pep440_normalized_release_candidate(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    fake = SimpleNamespace(
        metadata={"Name": "singleaxis-fabric"},
        version=__version__.replace("-rc.", "rc"),
    )
    monkeypatch.setattr("fabric.cli.metadata.distribution", lambda _name: fake)
    check = cli.run_checks(_healthy_env())[1]
    assert check.status == "pass"
    assert check.id == "package.identity"
