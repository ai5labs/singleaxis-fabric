# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Release-boundary tests for the recorder-first OSS artifact set."""

from __future__ import annotations

import json
import re
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[2]


def test_release_policy_is_recorder_only() -> None:
    policy = json.loads(
        (ROOT / "scripts/release/release-policy.json").read_text(encoding="utf-8")
    )
    assert policy["helm"] == {
        "first_party_app_charts": ["otel-collector"],
        "third_party_app_charts": [],
    }
    assert policy["images"] == ["fabric-otelcol"]
    assert policy["python_distribution"]["required_console_scripts"] == {}
    forbidden = set(policy["python_distribution"]["forbidden_wheel_paths"])
    assert {
        "fabric/guardrails.py",
        "fabric/judge.py",
        "fabric/policy.py",
        "fabric/presidio.py",
        "fabric/tool_auth.py",
    }.issubset(forbidden)
    assert set(policy["contracts"]["public_families"]) == {
        "activity",
        "connect",
        "delivery",
        "privacy",
        "recorder",
    }
    assert policy["contracts"]["public_versions"] == {
        "activity": ["v2"],
        "connect": ["v1"],
        "delivery": ["v1"],
        "privacy": ["v1"],
        "recorder": ["v1"],
    }
    assert policy["required_workflows"] == [
        "recorder-ci.yml",
        "recorder-security.yml",
        "codeql.yml",
        "recorder-license.yml",
        "e2e.yml",
    ]


def test_required_workflows_qualify_the_recorder_on_main() -> None:
    for workflow_name in (
        "recorder-ci.yml",
        "recorder-security.yml",
        "recorder-license.yml",
        "e2e.yml",
    ):
        workflow = (ROOT / ".github/workflows" / workflow_name).read_text(
            encoding="utf-8"
        )
        assert "push:\n    branches: [main]" in workflow

    recorder_ci = (ROOT / ".github/workflows/recorder-ci.yml").read_text(
        encoding="utf-8"
    )
    for forbidden in (
        "presidio-sidecar",
        "nemo-sidecar",
        "prompt-guard-sidecar",
        "redteam-runner",
        "fabric-relay",
        "langfuse-bootstrap",
    ):
        assert forbidden not in recorder_ci
    assert not (ROOT / ".github/workflows/ci.yml").exists()
    assert not (ROOT / ".github/workflows/license.yml").exists()
    assert not (ROOT / ".github/workflows/security.yml").exists()


def test_release_workflow_does_not_publish_legacy_runtime_artifacts() -> None:
    workflow = (ROOT / ".github/workflows/release.yml").read_text(encoding="utf-8")
    for forbidden in (
        "publish-sidecar-images",
        "component: fabric-relay",
        "presidio-sidecar",
        "nemo-sidecar",
        "prompt-guard-sidecar",
        "redteam-runner",
        "langfuse-bootstrap",
        "path: .\n          format: spdx-json",
        "git archive --format=tar.gz",
    ):
        assert forbidden not in workflow
    assert "component: otel-collector-fabric" in workflow
    assert "sbom: true" in workflow
    assert "provenance: true" in workflow


def test_fabric_node_binary_manifest_contains_no_legacy_processors() -> None:
    manifest = (ROOT / "components/otel-collector-fabric/ocb-config.yaml").read_text(
        encoding="utf-8"
    )
    assert "fabricguardprocessor" in manifest
    for forbidden in (
        "fabricpolicyprocessor",
        "fabricredactprocessor",
        "fabricsamplerprocessor",
    ):
        assert forbidden not in manifest


def test_umbrella_declares_only_collector_dependency() -> None:
    chart_text = (ROOT / "charts/fabric/Chart.yaml").read_text(encoding="utf-8")
    dependency_text = chart_text.split("\ndependencies:\n", maxsplit=1)[1]
    assert re.findall(r"(?m)^  - name: ([^\s]+)", dependency_text) == ["otel-collector"]
    for forbidden in (
        "fabric-relay",
        "presidio-sidecar",
        "nemo-sidecar",
        "redteam-runner",
        "langfuse",
        "update-agent",
    ):
        assert forbidden not in chart_text


def test_compose_harness_prepares_non_root_queue_and_qualifies_new_records() -> None:
    compose = yaml.safe_load(
        (ROOT / "deploy/compose/docker-compose.yml").read_text(encoding="utf-8")
    )
    services = compose["services"]
    queue_init = services["queue-init"]
    assert queue_init["user"] == "0:0"
    assert queue_init["restart"] == "no"
    assert any("fabric-queue:/queue" in volume for volume in queue_init["volumes"])
    assert "chown -R 65532:65532 /queue" in " ".join(queue_init["command"])
    assert services["fabric-node"]["depends_on"]["queue-init"]["condition"] == (
        "service_completed_successfully"
    )

    qualifier = (ROOT / "deploy/compose/qualify.sh").read_text(encoding="utf-8")
    assert "MUST_NOT_LEAVE_FABRIC_NODE&after=${before}" in qualifier
    assert "FABRIC_E2E_RECONSTRUCTION_METADATA&after=${before}" in qualifier


def test_e2e_workflow_runs_the_deterministic_agentic_critic() -> None:
    workflow = (ROOT / ".github/workflows/e2e.yml").read_text(encoding="utf-8")
    assert "agentic-shadow-workflow.json" in workflow
    assert "scenarios/agentic-shadow-outage.json" in workflow
    assert "python deploy/compose/critic.py" in workflow
    assert "recorder-e2e-critic-evidence" in workflow

    scenario = json.loads(
        (ROOT / "deploy/compose/scenarios/agentic-shadow-outage.json").read_text(
            encoding="utf-8"
        )
    )
    assert scenario["required_claims"] == {
        "enforcement": False,
        "delivery_semantics": "at_least_once",
        "persistence_scope": "controlled_fsync_sink",
        "exactly_once": False,
        "arbitrary_destination_persistence": False,
    }
