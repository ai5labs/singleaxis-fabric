# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Guard customer-facing recorder documentation against product-plane drift."""

from __future__ import annotations

from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]

PRIMARY_DOCS = (
    "README.md",
    "docs/README.md",
    "docs/architecture.md",
    "docs/install.md",
    "docs/quickstart.md",
    "docs/deployment.md",
    "docs/integration-models.md",
    "docs/capturing-interactions.md",
    "docs/exporting-to-your-observability-backend.md",
    "docs/enterprise-readiness.md",
    "docs/auditor-checklist.md",
    "docs/loom-walkthrough.md",
    "docs/how-fabric-fits-in-your-agent-stack.md",
    "docs/regulatory-profiles.md",
    "docs/operations/dr.md",
    "docs/api-stability.md",
    "docs/building-fabric.md",
    "docs/typescript-parity-backlog.md",
    "docs/verify-release.md",
    "docs/recorder-v1-qualification-status.md",
)

STALE_POSITIVE_CLAIMS = (
    "eu-ai-act-high-risk",
    "permissive-dev",
    "fabricredactprocessor",
    "fabricsamplerprocessor",
    "ToolAuthorizer",
    "record_eval",
    "queue_judge",
    "757 tests",
    "Inline PII redaction",
    "Fail-closed guardrails",
)


def test_primary_recorder_docs_do_not_restore_legacy_product_claims() -> None:
    for relative in PRIMARY_DOCS:
        text = (ROOT / relative).read_text(encoding="utf-8")
        for stale in STALE_POSITIVE_CLAIMS:
            assert stale not in text, (
                f"{relative} contains stale recorder claim {stale!r}"
            )


def test_downstream_design_docs_are_prominently_scoped() -> None:
    assurance = (ROOT / "docs/assurance-findings.md").read_text(encoding="utf-8")
    graph = (ROOT / "docs/decision-graph.md").read_text(encoding="utf-8")
    superseded = (ROOT / "specs/025-product-planes-and-packaging.md").read_text(
        encoding="utf-8"
    )
    assert "Platform design, not recorder v1" in assurance[:600]
    assert "SingleAxis Platform, not the OSS recorder runtime" in graph[:600]
    assert "Superseded. Do not implement recorder v1" in superseded[:700]


def test_readme_describes_the_actual_recorder_cli() -> None:
    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    assert (
        "Local recorder initialization, configuration validation, digest, help, and version"
        in readme
    )
    assert "deployment receipts" not in readme
