#!/usr/bin/env python3
# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Run a realistic, deterministic ambient-clinical shadow workflow.

The agent workflow is simulated, while the Fabric SDK, OpenTelemetry exporter,
Fabric Node Collector, privacy processor and delivery path are the real release
components. No network service, credential or patient data is required.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path

from fabric import Fabric, FabricConfig, ReplayBehavior, SideEffectType
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.sdk.trace import TracerProvider

from fabric import install_default_provider


WORKFLOW_ID = "ambient-clinical-documentation-shadow"
AGENT_ID = "ambient-clinical-note-agent"
DEPLOYMENT_ID = "hospital-shadow-deployment"
SENSITIVE_MARKER = "SYNTHETIC_CLINICAL_TEXT_MUST_NOT_CROSS_BOUNDARY"


def _sha256(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def run(endpoint: str, run_id: str) -> dict[str, object]:
    """Emit one complete agent execution and return expected safe evidence."""
    exporter = OTLPSpanExporter(endpoint=endpoint, timeout=10)
    provider = install_default_provider(
        service_name="ambient-clinical-note-agent",
        exporter=exporter,
        resource_attributes={
            "service.namespace": "singleaxis-e2e",
            "service.version": "scenario-v1",
            "deployment.environment.name": "test",
        },
    )
    if not isinstance(provider, TracerProvider):  # pragma: no cover - defensive
        raise RuntimeError("a real OpenTelemetry TracerProvider was not installed")

    execution_id = f"execution-{run_id}"
    decision_id = f"decision-{run_id}"
    tool_call_id = f"fhir-read-{run_id}"
    side_effect_id = f"ehr-draft-{run_id}"
    retrieval_query = f"FHIR patient context for synthetic encounter {run_id}"
    retrieval_result = '{"resourceType":"Bundle","total":1}'
    tool_arguments = json.dumps(
        {"encounter_ref": f"Encounter/{run_id}", "patient_excerpt": SENSITIVE_MARKER},
        separators=(",", ":"),
        sort_keys=True,
    )
    tool_result = json.dumps(
        {"resource_type": "Patient", "synthetic": True},
        separators=(",", ":"),
        sort_keys=True,
    )
    draft_request = json.dumps(
        {
            "resourceType": "DocumentReference",
            "status": "current",
            "description": SENSITIVE_MARKER,
        },
        separators=(",", ":"),
        sort_keys=True,
    )
    draft_result = json.dumps(
        {"staging_id": f"staging-{run_id}", "status": "PENDING_REVIEW"},
        separators=(",", ":"),
        sort_keys=True,
    )

    client = Fabric(
        FabricConfig(
            tenant_id="synthetic-health-system",
            agent_id=AGENT_ID,
            agent_name="Ambient Clinical Note Agent",
            agent_version="scenario-v1",
            profile="shadow",
            workflow_id=WORKFLOW_ID,
        )
    )

    with client.execution(
        execution_id=execution_id,
        workflow_id=WORKFLOW_ID,
        execution_attempt_id=f"attempt-{run_id}",
        execution_attempt=1,
    ):
        with client.decision(
            session_id=f"encounter-{run_id}",
            request_id=f"ambient-turn-{run_id}",
            decision_id=decision_id,
            workflow_name="Ambient note generation and EHR draft staging",
            attributes={
                "event_class": "activity",
                "fabric.system_id": "ambient-clinical-documentation",
                "fabric.deployment_id": DEPLOYMENT_ID,
                "fabric.environment": "test",
                "fabric.release_id": "scenario-v1",
                "fabric.capture.source": "sdk",
                "fabric.content_mode": "metadata",
            },
        ) as decision:
            # Simulate an upstream integration that accidentally adds raw text.
            # Fabric Node must remove this non-allowlisted field before export.
            decision.span.set_attribute("gen_ai.prompt", SENSITIVE_MARKER)

            decision.record_retrieval(
                "document",
                query=retrieval_query,
                result_count=1,
                result_hashes=[_sha256(retrieval_result)],
                source_document_ids=[f"Patient/{run_id}"],
                latency_ms=12,
                data_source_id="fhir-r4-patient-context",
                provider="synthetic-fhir",
            )

            with decision.llm_call(
                provider="synthetic-model-provider",
                model="clinical-note-model",
                max_tokens=256,
                step_id="draft-clinical-note",
            ) as model_call:
                model_call.set_usage(
                    input_tokens=128,
                    output_tokens=48,
                    finish_reason="stop",
                )
                model_call.set_response(model="clinical-note-model")

            with decision.tool_call(
                "read_fhir_patient_context",
                call_id=tool_call_id,
                tool_type="http",
                step_id="read-patient-context",
            ) as tool_call:
                tool_call.set_arguments(tool_arguments)
                tool_call.set_result(tool_result)
                tool_call.set_result_count(1)

            decision.record_side_effect(
                SideEffectType.EXTERNAL_WRITE,
                target_system="ehr-shadow-staging",
                operation="FHIR R4 DocumentReference.create",
                request_payload=draft_request,
                result_payload=draft_result,
                idempotency_key=f"draft-{run_id}",
                approval_required=True,
                committed=False,
                rollback_supported=True,
                replay_behavior=ReplayBehavior.MANUAL,
                parent_tool_call_id=tool_call_id,
                side_effect_id=side_effect_id,
            )
            decision.checkpoint(
                "clinical-note-draft-staged",
                state_hash=_sha256(draft_result),
            )

    if not provider.force_flush(timeout_millis=10_000):
        raise RuntimeError("timed out flushing the healthcare shadow trace")
    provider.shutdown()

    return {
        "run_id": run_id,
        "workflow_id": WORKFLOW_ID,
        "execution_id": execution_id,
        "decision_id": decision_id,
        "agent_id": AGENT_ID,
        "deployment_id": DEPLOYMENT_ID,
        "tool_call_id": tool_call_id,
        "side_effect_id": side_effect_id,
        "expected_hashes": {
            "retrieval_query": _sha256(retrieval_query),
            "tool_arguments": _sha256(tool_arguments),
            "tool_result": _sha256(tool_result),
            "side_effect_request": _sha256(draft_request),
            "side_effect_result": _sha256(draft_result),
        },
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--manifest", type=Path, required=True)
    args = parser.parse_args()
    manifest = run(args.endpoint, args.run_id)
    args.manifest.parent.mkdir(parents=True, exist_ok=True)
    args.manifest.write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    print(json.dumps(manifest, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
