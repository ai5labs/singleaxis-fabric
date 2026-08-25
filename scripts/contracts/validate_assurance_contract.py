# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Validate the digest-pinned public AssuranceFinding v1 contract."""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path, PurePosixPath
from typing import Any, Iterable, Mapping, Sequence

from jsonschema import Draft202012Validator, FormatChecker


@dataclass(frozen=True)
class AssuranceValidationError(ValueError):
    """Stable, automation-safe AssuranceFinding validation failure."""

    code: str
    path: str
    message: str

    def __str__(self) -> str:
        return f"{self.code}: {self.path}: {self.message}"


def _load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise AssuranceValidationError(
            "assurance.document.unreadable", str(path), str(exc)
        ) from exc
    if not isinstance(value, dict):
        raise AssuranceValidationError(
            "assurance.document.not_object",
            str(path),
            "document must be a JSON object",
        )
    return value


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _contract_path(root: Path, relative: object) -> Path:
    if not isinstance(relative, str) or not relative:
        raise AssuranceValidationError(
            "assurance.index.invalid_path",
            "manifest.json",
            "artifact path must be a non-empty string",
        )
    candidate = PurePosixPath(relative)
    if candidate.is_absolute() or ".." in candidate.parts or "." in candidate.parts:
        raise AssuranceValidationError(
            "assurance.index.invalid_path",
            relative,
            "path must be normalized and contract-relative",
        )
    resolved = root.joinpath(*candidate.parts)
    if not resolved.is_file() or resolved.is_symlink():
        raise AssuranceValidationError(
            "assurance.index.missing_artifact",
            relative,
            "pinned regular file does not exist",
        )
    return resolved


def _schema_path(parts: Iterable[object]) -> str:
    rendered = "$"
    for part in parts:
        rendered += f"[{part}]" if isinstance(part, int) else f".{part}"
    return rendered


def _timestamp(value: str) -> datetime:
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def validate_finding(document: Mapping[str, Any], schema: Mapping[str, Any]) -> None:
    """Validate one finding against the schema and cross-field semantics."""

    errors = sorted(
        Draft202012Validator(schema, format_checker=FormatChecker()).iter_errors(
            document
        ),
        key=lambda error: tuple(str(part) for part in error.absolute_path),
    )
    if errors:
        error = errors[0]
        raise AssuranceValidationError(
            "assurance.schema.invalid",
            _schema_path(error.absolute_path),
            error.message,
        )

    result = document["result"]
    confidence = result["confidence"]
    basis = result["confidence_basis"]
    if (confidence is None) != (basis == "not_reported"):
        raise AssuranceValidationError(
            "assurance.semantic.confidence_basis",
            "$.result",
            "confidence must be null exactly when confidence_basis is not_reported",
        )
    if result["outcome"] == "pass" and result["severity"] != "none":
        raise AssuranceValidationError(
            "assurance.semantic.pass_severity",
            "$.result.severity",
            "a passing result cannot carry finding severity",
        )
    if result["outcome"] == "fail" and result["severity"] == "none":
        raise AssuranceValidationError(
            "assurance.semantic.fail_severity",
            "$.result.severity",
            "a failed result must carry a non-none severity",
        )

    timestamps = document["timestamps"]
    started = _timestamp(timestamps["run_started_at"])
    observed = _timestamp(timestamps["observed_at"])
    finalized = _timestamp(timestamps["finalized_at"])
    status_changed = _timestamp(timestamps["status_changed_at"])
    if not started <= observed <= finalized <= status_changed:
        raise AssuranceValidationError(
            "assurance.semantic.timestamp_order",
            "$.timestamps",
            "timestamps must satisfy run_started_at <= observed_at <= finalized_at <= status_changed_at",
        )
    appeal = document["appeal"]
    if appeal is not None:
        filed = _timestamp(appeal["filed_at"])
        if not finalized <= filed <= status_changed:
            raise AssuranceValidationError(
                "assurance.semantic.appeal_time",
                "$.appeal.filed_at",
                "appeal time must be between finding finalization and the appealed status record",
            )
    if document["status"] == "final" and status_changed != finalized:
        raise AssuranceValidationError(
            "assurance.semantic.final_status_time",
            "$.timestamps.status_changed_at",
            "a final finding status begins at finalization",
        )

    finding_id = document["finding_id"]
    links = {
        document["supersedes_finding_id"],
        document["superseded_by_finding_id"],
    }
    if finding_id in links:
        raise AssuranceValidationError(
            "assurance.semantic.self_reference",
            "$.finding_id",
            "a finding cannot supersede or be superseded by itself",
        )
    if None not in links:
        raise AssuranceValidationError(
            "assurance.semantic.ambiguous_supersession",
            "$",
            "one finding cannot simultaneously supersede another and be superseded",
        )

    evidence_ids = [item["evidence_id"] for item in document["evidence_references"]]
    if len(evidence_ids) != len(set(evidence_ids)):
        raise AssuranceValidationError(
            "assurance.semantic.duplicate_evidence",
            "$.evidence_references",
            "evidence_id values must be unique within a finding",
        )

    provenance = document["evaluator_provenance"]
    if (
        document["source_type"] == "red_team"
        and provenance["execution_mode"] == "model"
        and provenance["model"] is None
    ):
        raise AssuranceValidationError(
            "assurance.semantic.red_team_model_provenance",
            "$.evaluator_provenance.model",
            "model-executed red-team findings require model provenance",
        )

    incident_id = document["subject"]["incident_id"]
    if document["lifecycle_stage"] != "incident" and incident_id is not None:
        raise AssuranceValidationError(
            "assurance.semantic.incident_scope",
            "$.subject.incident_id",
            "incident_id is reserved for incident-stage findings",
        )


def validate_contract(root: Path) -> list[str]:
    """Verify the pinned index and every positive and negative fixture."""

    root = root.resolve()
    index = _load_json(root / "manifest.json")
    if (
        index.get("contract") != "singleaxis.fabric.assurance-finding"
        or index.get("version") != "1.0.0"
    ):
        raise AssuranceValidationError(
            "assurance.index.identity",
            "manifest.json",
            "unsupported contract identity or version",
        )

    schema_record = index.get("schema")
    if not isinstance(schema_record, dict):
        raise AssuranceValidationError(
            "assurance.index.schema", "manifest.json", "schema record is required"
        )
    schema_path = _contract_path(root, schema_record.get("path"))
    if schema_record.get("sha256") != _sha256(schema_path):
        raise AssuranceValidationError(
            "assurance.digest.mismatch",
            str(schema_record.get("path")),
            "SHA-256 digest does not match index",
        )
    schema = _load_json(schema_path)
    Draft202012Validator.check_schema(schema)

    records = index.get("artifacts")
    if not isinstance(records, list) or not records:
        raise AssuranceValidationError(
            "assurance.index.artifacts",
            "manifest.json",
            "non-empty artifacts array is required",
        )
    seen_paths: set[str] = set()
    seen_findings: set[str] = set()
    validated: list[str] = []
    for record in records:
        if not isinstance(record, dict):
            raise AssuranceValidationError(
                "assurance.index.artifact",
                "manifest.json",
                "each artifact record must be an object",
            )
        relative = record.get("path")
        path = _contract_path(root, relative)
        if relative in seen_paths:
            raise AssuranceValidationError(
                "assurance.index.duplicate_path",
                str(relative),
                "artifact path is listed more than once",
            )
        seen_paths.add(str(relative))
        if record.get("sha256") != _sha256(path):
            raise AssuranceValidationError(
                "assurance.digest.mismatch",
                str(relative),
                "SHA-256 digest does not match index",
            )
        document = _load_json(path)
        expectation = record.get("expectation")
        if expectation == "valid":
            validate_finding(document, schema)
            finding_id = document["finding_id"]
            if finding_id in seen_findings:
                raise AssuranceValidationError(
                    "assurance.index.duplicate_finding",
                    str(relative),
                    f"duplicate finding_id {finding_id!r}",
                )
            seen_findings.add(finding_id)
        elif expectation == "invalid":
            error_code = record.get("error_code")
            if not isinstance(error_code, str) or not error_code:
                raise AssuranceValidationError(
                    "assurance.index.invalid_fixture",
                    str(relative),
                    "invalid fixture requires error_code",
                )
            try:
                validate_finding(document, schema)
            except AssuranceValidationError as exc:
                if exc.code != error_code:
                    raise AssuranceValidationError(
                        "assurance.fixture.wrong_error",
                        str(relative),
                        f"expected {error_code}, received {exc.code}",
                    ) from exc
            else:
                raise AssuranceValidationError(
                    "assurance.fixture.unexpectedly_valid",
                    str(relative),
                    "negative fixture passed validation",
                )
        else:
            raise AssuranceValidationError(
                "assurance.index.expectation",
                str(relative),
                "expectation must be valid or invalid",
            )
        validated.append(str(relative))

    indexed = {"manifest.json", str(schema_record["path"]), *seen_paths}
    present = {
        path.relative_to(root).as_posix()
        for path in root.rglob("*.json")
        if path.is_file() and not path.is_symlink()
    }
    if present != indexed:
        raise AssuranceValidationError(
            "assurance.index.coverage",
            "manifest.json",
            f"unpinned={sorted(present - indexed)}; missing={sorted(indexed - present)}",
        )
    return validated


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "root",
        nargs="?",
        type=Path,
        default=Path(__file__).resolve().parents[2] / "contracts" / "assurance" / "v1",
        help="path to contracts/assurance/v1",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        validated = validate_contract(args.root)
    except Exception as exc:
        print(f"assurance contract validation failed: {exc}", file=sys.stderr)
        return 1
    print(f"assurance contract valid: {len(validated)} pinned fixtures")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
