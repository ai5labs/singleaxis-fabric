#!/usr/bin/env python3
# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Verify the real Fabric path for the ambient-clinical shadow workflow."""

from __future__ import annotations

import argparse
import base64
import json
from pathlib import Path
from typing import Any

from opentelemetry.proto.collector.trace.v1.trace_service_pb2 import (
    ExportTraceServiceRequest,
)

from workload import SENSITIVE_MARKER


def _load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain a JSON object")
    return value


def _value(value: Any) -> Any:
    selected = value.WhichOneof("value")
    if selected == "array_value":
        return tuple(_value(item) for item in value.array_value.values)
    if selected == "kvlist_value":
        return {item.key: _value(item.value) for item in value.kvlist_value.values}
    return getattr(value, selected) if selected else None


def _attributes(items: Any) -> dict[str, Any]:
    return {item.key: _value(item.value) for item in items}


def _spans(records: dict[str, Any]) -> tuple[list[Any], list[bytes]]:
    spans: list[Any] = []
    bodies: list[bytes] = []
    raw_records = records.get("records")
    if not isinstance(raw_records, list) or not raw_records:
        raise ValueError("controlled sink returned no OTLP records")
    for record in raw_records:
        if not isinstance(record, dict):
            raise ValueError("sink record must be an object")
        body = base64.b64decode(record["body_base64"], validate=True)
        bodies.append(body)
        request = ExportTraceServiceRequest()
        request.ParseFromString(body)
        for resource_spans in request.resource_spans:
            for scope_spans in resource_spans.scope_spans:
                spans.extend(scope_spans.spans)
    return spans, bodies


def verify(
    records: dict[str, Any], manifests: list[dict[str, Any]], runtime: dict[str, Any]
) -> dict[str, Any]:
    spans, bodies = _spans(records)
    checks: list[dict[str, object]] = []

    def check(check_id: str, condition: bool, detail: str) -> None:
        checks.append({"id": check_id, "passed": bool(condition), "detail": detail})

    check(
        "protect.raw_content_removed",
        all(SENSITIVE_MARKER.encode() not in body for body in bodies),
        "synthetic raw clinical text is absent from exported OTLP bytes",
    )
    check(
        "delivery.baseline_observed",
        runtime["baseline_after"] > runtime["baseline_before"],
        "the normal shadow run reached the controlled destination",
    )
    check(
        "delivery.outage_exercised",
        runtime.get("destination_outage_observed") is True,
        "the destination was deliberately unavailable for the recovery run",
    )
    check(
        "delivery.collector_recreated",
        runtime["collector_uid_before"] != runtime["collector_uid_after"],
        "the Collector Kubernetes object identity changed during the outage",
    )
    check(
        "delivery.recovered_after_restart",
        runtime["recovery_after"] > runtime["recovery_before"],
        "queued telemetry arrived after destination and Collector recovery",
    )

    for manifest in manifests:
        run_id = str(manifest["run_id"])
        decision_matches = [
            span
            for span in spans
            if _attributes(span.attributes).get("fabric.decision_id")
            == manifest["decision_id"]
        ]
        check(
            f"{run_id}.decision.unique",
            len(decision_matches) == 1,
            "exactly one decision span is reconstructable for this execution",
        )
        if len(decision_matches) != 1:
            continue
        decision = decision_matches[0]
        trace_spans = [span for span in spans if span.trace_id == decision.trace_id]
        attrs = _attributes(decision.attributes)
        expected_identity = {
            "fabric.tenant_id": "synthetic-health-system",
            "fabric.agent_id": manifest["agent_id"],
            "fabric.deployment_id": manifest["deployment_id"],
            "fabric.execution_id": manifest["execution_id"],
            "fabric.workflow_id": manifest["workflow_id"],
            "fabric.capture.source": "sdk",
            "fabric.content_mode": "metadata",
        }
        check(
            f"{run_id}.identity.preserved",
            all(attrs.get(key) == value for key, value in expected_identity.items()),
            "tenant, agent, deployment, execution, workflow and capture identity survived",
        )
        check(
            f"{run_id}.raw_prompt_attribute_absent",
            "gen_ai.prompt" not in attrs,
            "the upstream raw prompt attribute was removed",
        )

        execution = [
            span
            for span in trace_spans
            if span.name == "fabric.execution"
            and _attributes(span.attributes).get("fabric.execution_id")
            == manifest["execution_id"]
        ]
        check(
            f"{run_id}.execution.present",
            len(execution) == 1,
            "the execution root is present",
        )
        if execution:
            check(
                f"{run_id}.decision.parented",
                decision.parent_span_id == execution[0].span_id,
                "the decision is causally parented to the execution",
            )

        expected_children = {
            "fabric.model_call",
            "fabric.retrieval",
            "fabric.tool_call",
        }
        actual_children = {
            span.name for span in trace_spans if span.parent_span_id == decision.span_id
        }
        check(
            f"{run_id}.children.complete",
            expected_children.issubset(actual_children),
            "model, retrieval and tool spans are direct decision children",
        )

        events = {
            event.name: _attributes(event.attributes) for event in decision.events
        }
        hashes = manifest["expected_hashes"]
        retrieval = events.get("fabric.retrieval", {})
        side_effect = events.get("fabric.side_effect", {})
        checkpoint = events.get("fabric.checkpoint", {})
        tool_spans = [
            span
            for span in trace_spans
            if span.name == "fabric.tool_call"
            and _attributes(span.attributes).get("gen_ai.tool.name")
            == "read_fhir_patient_context"
        ]
        tool_attrs = _attributes(tool_spans[0].attributes) if tool_spans else {}

        check(
            f"{run_id}.retrieval.hash_only",
            retrieval.get("fabric.retrieval.query_hash") == hashes["retrieval_query"],
            "the raw FHIR query is represented by its local SHA-256 digest",
        )
        check(
            f"{run_id}.tool.hash_only",
            tool_attrs.get("fabric.tool.arguments_hash") == hashes["tool_arguments"]
            and tool_attrs.get("fabric.tool.result_hash") == hashes["tool_result"],
            "tool arguments and results are represented by local hashes",
        )
        check(
            f"{run_id}.side_effect.reconstructable",
            side_effect.get("fabric.side_effect.side_effect_id")
            == manifest["side_effect_id"]
            and side_effect.get("fabric.side_effect.target_system")
            == "ehr-shadow-staging"
            and side_effect.get("fabric.side_effect.committed") is False
            and side_effect.get("fabric.side_effect.request_hash")
            == hashes["side_effect_request"]
            and side_effect.get("fabric.side_effect.result_hash")
            == hashes["side_effect_result"],
            "the held EHR draft is correlated, non-committed and hash-only",
        )
        check(
            f"{run_id}.checkpoint.present",
            checkpoint.get("fabric.checkpoint.step_name")
            == "clinical-note-draft-staged",
            "the workflow checkpoint survived for reconstruction",
        )

    return {
        "passed": all(bool(item["passed"]) for item in checks),
        "checks": checks,
        "summary": {
            "passed": sum(bool(item["passed"]) for item in checks),
            "total": len(checks),
        },
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--records", type=Path, required=True)
    parser.add_argument("--manifest", type=Path, action="append", required=True)
    parser.add_argument("--runtime", type=Path, required=True)
    parser.add_argument("--report", type=Path, required=True)
    args = parser.parse_args()

    report = verify(
        _load(args.records),
        [_load(path) for path in args.manifest],
        _load(args.runtime),
    )
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(
        json.dumps(report, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    print(json.dumps(report, indent=2, sort_keys=True))
    if not report["passed"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
