# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Shared harness for running scenarios and loading/storing goldens.

Both the pytest runner (``test_conformance.py``) and the regeneration
entrypoint (``generate.py``) go through this module so the exact same
normalization is used to produce and to assert the goldens.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path
from typing import TYPE_CHECKING, Any

from .normalize import normalize_spans
from .scenarios import SCENARIOS

if TYPE_CHECKING:
    from opentelemetry.sdk.trace.export.in_memory_span_exporter import (
        InMemorySpanExporter,
    )

CONTRACT_DIR = Path(__file__).resolve().parents[4] / "contracts" / "activity" / "v1"
MANIFEST_PATH = CONTRACT_DIR / "manifest.json"
GOLDENS_DIR = CONTRACT_DIR / "goldens"
SCHEMA_DIR = CONTRACT_DIR / "schema"

# Stable JSON serialization options for reproducible files.
_JSON_KWARGS: dict[str, Any] = {"indent": 2, "sort_keys": True, "ensure_ascii": False}


def golden_path(name: str) -> Path:
    """Return the on-disk path of the golden for scenario ``name``."""
    entry = scenario_manifest(name)
    return CONTRACT_DIR / str(entry["fixture"])


def load_manifest() -> dict[str, Any]:
    """Load the public activity-contract manifest."""
    manifest: dict[str, Any] = json.loads(MANIFEST_PATH.read_text(encoding="utf-8"))
    return manifest


def scenario_manifest(name: str) -> dict[str, Any]:
    """Return the unique manifest entry for ``name``."""
    matches = [item for item in load_manifest()["scenarios"] if item["name"] == name]
    if len(matches) != 1:
        raise KeyError(f"contract manifest must declare scenario {name!r} exactly once")
    entry: dict[str, Any] = matches[0]
    return entry


def sha256_file(path: Path) -> str:
    """Return the lowercase SHA-256 digest for a contract artifact."""
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run_scenario(name: str, exporter: InMemorySpanExporter) -> list[dict[str, Any]]:
    """Run one scenario and return its normalized span list.

    The caller owns the exporter (and the global tracer provider it is
    wired into). The exporter is cleared before and the finished spans
    captured after, so each scenario sees a clean slate.
    """
    if name not in SCENARIOS:
        raise KeyError(f"unknown scenario: {name!r}")
    exporter.clear()
    SCENARIOS[name]()
    spans = list(exporter.get_finished_spans())
    exporter.clear()
    return normalize_spans(spans)


def load_golden(name: str) -> list[dict[str, Any]]:
    """Load the stored golden for scenario ``name``."""
    result: list[dict[str, Any]] = json.loads(golden_path(name).read_text(encoding="utf-8"))
    return result


def dump_golden(name: str, normalized: list[dict[str, Any]]) -> None:
    """Write ``normalized`` as the golden for scenario ``name``."""
    GOLDENS_DIR.mkdir(parents=True, exist_ok=True)
    text = json.dumps(normalized, **_JSON_KWARGS) + "\n"
    golden_path(name).write_text(text, encoding="utf-8")


def serialize(normalized: list[dict[str, Any]]) -> str:
    """Serialize a normalized span list with the canonical JSON options."""
    return json.dumps(normalized, **_JSON_KWARGS)
