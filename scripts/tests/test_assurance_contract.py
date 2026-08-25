# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Qualification tests for the public AssuranceFinding v1 contract."""

from __future__ import annotations

import copy
import json
import shutil
import sys
from pathlib import Path
from typing import Any

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]
CONTRACT_ROOT = REPO_ROOT / "contracts" / "assurance" / "v1"
sys.path.insert(0, str(REPO_ROOT / "scripts"))

from contracts.validate_assurance_contract import (  # noqa: E402
    AssuranceValidationError,
    validate_contract,
    validate_finding,
)


def _json(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    assert isinstance(value, dict)
    return value


def _schema() -> dict[str, Any]:
    return _json(CONTRACT_ROOT / "schema" / "assurance-finding-v1.schema.json")


def _finding(relative: str) -> dict[str, Any]:
    return _json(CONTRACT_ROOT / relative)


def test_repository_contract_and_all_fixtures_validate() -> None:
    validated = validate_contract(CONTRACT_ROOT)
    assert len(validated) == 11
    assert "fixtures/valid/runtime-llm-judge.json" in validated
    assert "fixtures/invalid/raw-prompt-field.json" in validated


@pytest.mark.parametrize(
    ("relative", "code"),
    [
        (
            "fixtures/invalid/confidence-mismatch.json",
            "assurance.semantic.confidence_basis",
        ),
        (
            "fixtures/invalid/timestamp-order.json",
            "assurance.semantic.timestamp_order",
        ),
        (
            "fixtures/invalid/self-supersession.json",
            "assurance.semantic.self_reference",
        ),
        (
            "fixtures/invalid/red-team-model-missing.json",
            "assurance.semantic.red_team_model_provenance",
        ),
        (
            "fixtures/invalid/runtime-correlation-missing.json",
            "assurance.schema.invalid",
        ),
        (
            "fixtures/invalid/raw-prompt-field.json",
            "assurance.schema.invalid",
        ),
    ],
)
def test_negative_fixtures_fail_with_stable_code(relative: str, code: str) -> None:
    with pytest.raises(AssuranceValidationError) as caught:
        validate_finding(_finding(relative), _schema())
    assert caught.value.code == code


def test_predeployment_does_not_invent_runtime_identifiers() -> None:
    finding = _finding("fixtures/valid/predeployment-deterministic.json")
    subject = finding["subject"]
    assert subject["deployment_id"] is None
    assert subject["execution_id"] is None
    assert subject["decision_id"] is None
    assert subject["trace_id"] is None
    assert subject["span_id"] is None
    validate_finding(finding, _schema())


def test_runtime_requires_deployment_and_one_causal_identifier() -> None:
    finding = copy.deepcopy(_finding("fixtures/valid/runtime-llm-judge.json"))
    subject = finding["subject"]
    subject["deployment_id"] = None
    subject["execution_id"] = None
    subject["decision_id"] = None
    subject["trace_id"] = None
    subject["span_id"] = None
    with pytest.raises(AssuranceValidationError) as caught:
        validate_finding(finding, _schema())
    assert caught.value.code == "assurance.schema.invalid"


def test_span_correlation_requires_trace_id() -> None:
    finding = copy.deepcopy(_finding("fixtures/valid/predeployment-red-team.json"))
    finding["subject"]["trace_id"] = None
    with pytest.raises(AssuranceValidationError) as caught:
        validate_finding(finding, _schema())
    assert caught.value.code == "assurance.schema.invalid"


def test_llm_judge_requires_rubric_and_model_provenance() -> None:
    finding = copy.deepcopy(_finding("fixtures/valid/runtime-llm-judge.json"))
    finding["versions"]["rubric_version"] = None
    finding["evaluator_provenance"]["model"] = None
    with pytest.raises(AssuranceValidationError) as caught:
        validate_finding(finding, _schema())
    assert caught.value.code == "assurance.schema.invalid"


def test_status_fixtures_cover_final_appealed_and_superseded() -> None:
    statuses = {
        _json(path)["status"]
        for path in (CONTRACT_ROOT / "fixtures" / "valid").glob("*.json")
    }
    assert statuses == {"final", "appealed", "superseded"}


def test_valid_findings_have_references_not_inline_content() -> None:
    forbidden = {"prompt", "completion", "raw_prompt", "raw_completion", "content"}

    def keys(value: object) -> set[str]:
        if isinstance(value, dict):
            return set(value) | {key for child in value.values() for key in keys(child)}
        if isinstance(value, list):
            return {key for child in value for key in keys(child)}
        return set()

    for path in (CONTRACT_ROOT / "fixtures" / "valid").glob("*.json"):
        assert not (keys(_json(path)) & forbidden), path.name


def test_digest_tampering_fails_closed(tmp_path: Path) -> None:
    copied = tmp_path / "contract"
    shutil.copytree(CONTRACT_ROOT, copied)
    path = copied / "fixtures" / "valid" / "runtime-llm-judge.json"
    finding = _json(path)
    finding["result"]["score"] = 0.99
    path.write_text(json.dumps(finding), encoding="utf-8")
    with pytest.raises(AssuranceValidationError) as caught:
        validate_contract(copied)
    assert caught.value.code == "assurance.digest.mismatch"


def test_unpinned_json_fails_closed(tmp_path: Path) -> None:
    copied = tmp_path / "contract"
    shutil.copytree(CONTRACT_ROOT, copied)
    (copied / "fixtures" / "valid" / "unreviewed.json").write_text(
        "{}\n", encoding="utf-8"
    )
    with pytest.raises(AssuranceValidationError) as caught:
        validate_contract(copied)
    assert caught.value.code == "assurance.index.coverage"


def test_finding_ids_are_unique_across_positive_fixtures() -> None:
    identifiers = [
        _json(path)["finding_id"]
        for path in (CONTRACT_ROOT / "fixtures" / "valid").glob("*.json")
    ]
    assert len(identifiers) == len(set(identifiers))
