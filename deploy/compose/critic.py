#!/usr/bin/env python3
# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Deterministic critic for the recorder's passive-shadow E2E scenario.

This is release-test tooling, not an LLM judge or a runtime assurance engine.
It fail-closes over structured observations produced by the Compose or kind
workflow and prevents the test from making stronger delivery claims than the
controlled evidence supports.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


SCENARIO_SCHEMA = "fabric.recorder-e2e-scenario/v1"
EVIDENCE_SCHEMA = "fabric.recorder-e2e-evidence/v1"
REPORT_SCHEMA = "fabric.recorder-e2e-critic-report/v1"
SAFE_CLAIMS: dict[str, object] = {
    "enforcement": False,
    "delivery_semantics": "at_least_once",
    "persistence_scope": "controlled_fsync_sink",
    "exactly_once": False,
    "arbitrary_destination_persistence": False,
}


class CriticInputError(ValueError):
    """Raised when scenario or evidence input is malformed."""


def _object(value: object, name: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise CriticInputError(f"{name} must be a JSON object")
    return value


def _integer(value: object, name: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise CriticInputError(f"{name} must be a non-negative integer")
    return value


def _boolean(value: object, name: str) -> bool:
    if not isinstance(value, bool):
        raise CriticInputError(f"{name} must be a boolean")
    return value


def _text(value: object, name: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise CriticInputError(f"{name} must be a non-empty string")
    return value


def _load(path: Path, name: str) -> dict[str, Any]:
    try:
        return _object(json.loads(path.read_text(encoding="utf-8")), name)
    except (OSError, json.JSONDecodeError) as exc:
        raise CriticInputError(f"cannot read {name}: {exc}") from exc


def criticize(scenario: dict[str, Any], evidence: dict[str, Any]) -> dict[str, Any]:
    """Return a machine-readable, fail-closed verdict for one E2E run."""
    if scenario.get("schema_version") != SCENARIO_SCHEMA:
        raise CriticInputError("unsupported scenario schema")
    if evidence.get("schema_version") != EVIDENCE_SCHEMA:
        raise CriticInputError("unsupported evidence schema")

    scenario_id = _text(scenario.get("scenario_id"), "scenario.scenario_id")
    if evidence.get("scenario_id") != scenario_id:
        raise CriticInputError("evidence scenario_id does not match scenario")

    raw_markers = scenario.get("required_reconstruction_markers")
    if not isinstance(raw_markers, list) or not raw_markers:
        raise CriticInputError(
            "scenario.required_reconstruction_markers must be non-empty"
        )
    markers = [_text(marker, "scenario marker") for marker in raw_markers]
    if len(set(markers)) != len(markers):
        raise CriticInputError("scenario reconstruction markers must be unique")

    required_claims = _object(
        scenario.get("required_claims"), "scenario.required_claims"
    )
    if required_claims != SAFE_CLAIMS:
        raise CriticInputError(
            "scenario required_claims may not weaken recorder semantics"
        )

    capture = _object(evidence.get("capture"), "evidence.capture")
    recovery = _object(evidence.get("recovery"), "evidence.recovery")
    claims = _object(evidence.get("claims"), "evidence.claims")
    observed_markers = _object(
        capture.get("reconstruction_markers"), "evidence.capture.reconstruction_markers"
    )

    checks: list[dict[str, object]] = []

    def check(check_id: str, passed: bool, detail: str) -> None:
        checks.append({"id": check_id, "passed": passed, "detail": detail})

    capture_before = _integer(capture.get("baseline_count"), "capture.baseline_count")
    capture_after = _integer(capture.get("delivered_count"), "capture.delivered_count")
    check(
        "capture.delivery_observed",
        capture_after > capture_before,
        "controlled sink count increased after the agent workflow",
    )
    forbidden_found = _boolean(
        capture.get("forbidden_marker_found"), "capture.forbidden_marker_found"
    )
    check(
        "protect.forbidden_content_absent",
        not forbidden_found,
        "forbidden content was absent from records created after the baseline",
    )
    for marker in markers:
        marker_found = _boolean(
            observed_markers.get(marker), f"capture.reconstruction_markers[{marker!r}]"
        )
        check(
            f"capture.marker.{marker}",
            marker_found,
            "required reconstruction marker reached the controlled sink",
        )

    outage_observed = _boolean(
        recovery.get("destination_outage_observed"),
        "recovery.destination_outage_observed",
    )
    check(
        "delivery.destination_outage_observed",
        outage_observed,
        "destination was unavailable when the recovery trace was accepted",
    )
    pod_before = _text(
        recovery.get("recorder_instance_before"), "recovery.recorder_instance_before"
    )
    pod_after = _text(
        recovery.get("recorder_instance_after"), "recovery.recorder_instance_after"
    )
    check(
        "delivery.recorder_restarted",
        pod_before != pod_after,
        "recorder instance identity changed before destination recovery",
    )
    recovery_before = _integer(
        recovery.get("baseline_count"), "recovery.baseline_count"
    )
    recovery_after = _integer(
        recovery.get("delivered_count"), "recovery.delivered_count"
    )
    check(
        "delivery.queued_record_recovered",
        recovery_after > recovery_before,
        "controlled sink count increased after outage and recorder restart",
    )

    for key, expected in SAFE_CLAIMS.items():
        check(
            f"claims.{key}",
            claims.get(key) == expected,
            f"claim is constrained to {expected!r}",
        )

    passed = all(bool(item["passed"]) for item in checks)
    return {
        "schema_version": REPORT_SCHEMA,
        "scenario_id": scenario_id,
        "passed": passed,
        "summary": {
            "passed_checks": sum(bool(item["passed"]) for item in checks),
            "total_checks": len(checks),
        },
        "checks": checks,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scenario", type=Path, required=True)
    parser.add_argument("--evidence", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)

    try:
        report = criticize(
            _load(args.scenario, "scenario"), _load(args.evidence, "evidence")
        )
    except CriticInputError as exc:
        report = {
            "schema_version": REPORT_SCHEMA,
            "scenario_id": "unknown",
            "passed": False,
            "summary": {"passed_checks": 0, "total_checks": 1},
            "checks": [{"id": "input.valid", "passed": False, "detail": str(exc)}],
        }

    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    print(json.dumps(report, indent=2, sort_keys=True))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
