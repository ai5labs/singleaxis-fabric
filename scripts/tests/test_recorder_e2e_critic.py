# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Tests for the deterministic recorder E2E critic."""

from __future__ import annotations

import importlib.util
import json
from pathlib import Path
from types import ModuleType

import pytest


ROOT = Path(__file__).resolve().parents[2]
CRITIC_PATH = ROOT / "deploy/compose/critic.py"
SCENARIO_PATH = ROOT / "deploy/compose/scenarios/agentic-shadow-outage.json"


def _load_critic() -> ModuleType:
    spec = importlib.util.spec_from_file_location(
        "fabric_recorder_e2e_critic", CRITIC_PATH
    )
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


CRITIC = _load_critic()
SCENARIO = json.loads(SCENARIO_PATH.read_text(encoding="utf-8"))


def _evidence() -> dict[str, object]:
    return {
        "schema_version": "fabric.recorder-e2e-evidence/v1",
        "scenario_id": "passive-agent-shadow-outage-recovery",
        "capture": {
            "baseline_count": 0,
            "delivered_count": 1,
            "forbidden_marker_found": False,
            "reconstruction_markers": {
                marker: True for marker in SCENARIO["required_reconstruction_markers"]
            },
        },
        "recovery": {
            "baseline_count": 1,
            "delivered_count": 2,
            "destination_outage_observed": True,
            "recorder_instance_before": "fabric-node-0-old",
            "recorder_instance_after": "fabric-node-0-new",
        },
        "claims": dict(SCENARIO["required_claims"]),
    }


def _failed_ids(report: dict[str, object]) -> set[str]:
    return {
        str(check["id"])
        for check in report["checks"]
        if isinstance(check, dict) and not check["passed"]
    }


def test_valid_agentic_shadow_evidence_passes() -> None:
    report = CRITIC.criticize(SCENARIO, _evidence())
    assert report["passed"] is True
    assert report["summary"] == {"passed_checks": 13, "total_checks": 13}


@pytest.mark.parametrize(
    ("mutation", "failed_check"),
    [
        (("capture", "delivered_count", 0), "capture.delivery_observed"),
        (
            ("capture", "forbidden_marker_found", True),
            "protect.forbidden_content_absent",
        ),
        (
            ("recovery", "destination_outage_observed", False),
            "delivery.destination_outage_observed",
        ),
        (
            ("recovery", "recorder_instance_after", "fabric-node-0-old"),
            "delivery.recorder_restarted",
        ),
        (("recovery", "delivered_count", 1), "delivery.queued_record_recovered"),
        (("claims", "exactly_once", True), "claims.exactly_once"),
    ],
)
def test_critic_fails_closed_on_false_observations(
    mutation: tuple[str, str, object], failed_check: str
) -> None:
    evidence = _evidence()
    section, key, value = mutation
    target = evidence[section]
    assert isinstance(target, dict)
    target[key] = value
    report = CRITIC.criticize(SCENARIO, evidence)
    assert report["passed"] is False
    assert failed_check in _failed_ids(report)


def test_missing_reconstruction_marker_is_invalid_input() -> None:
    evidence = _evidence()
    capture = evidence["capture"]
    assert isinstance(capture, dict)
    markers = capture["reconstruction_markers"]
    assert isinstance(markers, dict)
    markers.pop("get_patient_summary")
    with pytest.raises(CRITIC.CriticInputError, match="must be a boolean"):
        CRITIC.criticize(SCENARIO, evidence)


def test_scenario_cannot_weaken_delivery_claims() -> None:
    scenario = json.loads(json.dumps(SCENARIO))
    scenario["required_claims"]["exactly_once"] = True
    with pytest.raises(CRITIC.CriticInputError, match="may not weaken"):
        CRITIC.criticize(scenario, _evidence())
