# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Result emission — two signals, on purpose.

**Spans.** One ``fabric.redteam.run`` span per invocation and one
``fabric.redteam.probe`` child per :class:`~.results.ProbeResult`. These
are the detailed trail: per-probe verdicts, timings, and the
``fabric.control.*`` join key.

**Logs.** One ``red_team_result`` log record per (suite, suite_version)
in the run. This is the aggregate the collector's ``fabricguard``
allowlist has always had an entry for and which, until now, *nothing
emitted* — a dead allowlist entry and a whole missing signal.

Why both, rather than picking one:

* The collector's log path (``applyToRecord``) classifies records by an
  ``event_class`` attribute against a per-class field allowlist. The
  trace path (``processTraces``) filters by attribute-key *prefix* and
  never reads ``event_class`` at all. They are different mechanisms for
  different signals, and one record shape cannot satisfy both.
* The runner previously put ``event_class: "redteam_run"`` on **span**
  attributes and on the Resource. On the span it was pointless (the
  trace path ignores it) and actively harmful (bare ``event_class`` has
  no dotted prefix, so enabling trace processing strips it). On the
  Resource it was inert, since the log path reads the class off the
  *record*. The span attribute is now ``fabric.event_class``, which
  survives the prefix allowlist and no longer pretends to be the log
  discriminator; the Resource entry is gone.

The abstract :class:`ResultEmitter` lets tests and dry-runs swap the
OTel impl for an in-memory collector."""

from __future__ import annotations

import json
import logging
from collections.abc import Mapping, Sequence
from typing import Any, Protocol, runtime_checkable

from opentelemetry import trace
from opentelemetry._logs import Logger as OTelLogger
from opentelemetry._logs import SeverityNumber, get_logger
from opentelemetry.trace import SpanKind, Status, StatusCode, Tracer

from .results import ProbeResult, RunResult

_LOG = logging.getLogger(__name__)

# --- span attributes ----------------------------------------------------
#
# Every key is dotted and `fabric.`-prefixed so it survives the
# collector's DefaultTraceAttributePrefixes. A bare key (the old
# `event_class`) is stripped the moment trace processing is enabled.

ATTR_EVENT_CLASS = "fabric.event_class"
ATTR_RUN_ID = "fabric.redteam.run_id"
ATTR_RUN_TRACE_ID = "fabric.redteam.run_trace_id"
ATTR_RUN_SPAN_ID = "fabric.redteam.run_span_id"
ATTR_PROBE_SPAN_ID = "fabric.redteam.probe_span_id"
ATTR_TENANT = "fabric.tenant_id"
ATTR_AGENT = "fabric.agent_id"
ATTR_PROFILE = "fabric.profile"
# Renamed from `fabric.redteam.suite` / `.probe` to match the field
# names the bridge model and the collector allowlist already use
# (`suite_id`, `suite_version`). The old names were the only reason the
# two sides could not be diffed field-for-field.
ATTR_SUITE_ID = "fabric.redteam.suite_id"
ATTR_SUITE_VERSION = "fabric.redteam.suite_version"
ATTR_PROBE_ID = "fabric.redteam.probe_id"
ATTR_VERDICT = "fabric.redteam.verdict"
ATTR_DURATION_MS = "fabric.redteam.duration_ms"
ATTR_ATTEMPTS = "fabric.redteam.attempts"
ATTR_FINDINGS = "fabric.redteam.findings"
ATTR_FAIL_COUNT = "fabric.redteam.fail_count"
ATTR_PROBE_COUNT = "fabric.redteam.probe_count"

# --- control correlation (spec 024 §4) ---------------------------------

#: Singular form — exactly one control. The join key.
ATTR_CONTROL_ID = "fabric.control.id"
#: Plural form, for a record covering several controls. Deduplicated
#: and sorted so the value is stable across runs with equal coverage.
ATTR_CONTROL_IDS = "fabric.control.ids"
#: Present on BOTH sides of the join. Mismatched versions must fail the
#: join rather than silently re-point evidence at a remapped definition.
ATTR_CONTROL_CATALOG_VERSION = "fabric.control.catalog_version"
#: ``true`` when a probe resolved to no control. Always emitted, never
#: omitted: a missing attribute is indistinguishable from an old
#: producer, whereas ``false`` is a positive statement of coverage.
ATTR_CONTROL_UNMAPPED = "fabric.control.unmapped"

# --- log record ---------------------------------------------------------

#: The log-path discriminator. Bare (undotted) on purpose: it must match
#: the collector's `event_class_attribute` config, which defaults to
#: exactly this, and log records are not prefix-filtered.
LOG_ATTR_EVENT_CLASS = "event_class"
#: Value the collector's BuiltInAllowedFields keys on.
RED_TEAM_RESULT_CLASS = "red_team_result"

#: Exactly the field set of the bridge's ``RedTeamResult`` model and of
#: ``BuiltInAllowedFields["red_team_result"]``. Anything outside this set
#: is stripped by the collector; anything inside it that we fail to emit
#: is a silently missing column downstream. Asserted by
#: ``tests/test_emitter.py::test_red_team_result_record_matches_allowlist``.
RED_TEAM_RESULT_FIELDS: tuple[str, ...] = (
    "event_class",
    "tenant_id",
    "agent_id",
    "run_id",
    "suite_id",
    "suite_version",
    "started_at",
    "finished_at",
    "total_probes",
    "failed_probes",
    "severity_counts",
)


@runtime_checkable
class ResultEmitter(Protocol):
    def emit(self, result: RunResult) -> None: ...


@runtime_checkable
class LogRecordSink(Protocol):
    """Narrow seam over the OTel logs SDK.

    The logs SDK's ``Logger.emit`` signature has changed shape three
    times across the 1.x line; keeping the coupling to one method behind
    a protocol means a future change touches one class, and tests do not
    need a LoggerProvider at all."""

    def emit_record(self, *, body: str, attributes: Mapping[str, Any]) -> None: ...


class OTelLogSink:
    """Default sink — emits through the global OTel LoggerProvider."""

    def __init__(self, logger: OTelLogger | None = None) -> None:
        self._logger = logger or get_logger("fabric.redteam")

    def emit_record(self, *, body: str, attributes: Mapping[str, Any]) -> None:
        self._logger.emit(
            body=body,
            attributes=dict(attributes),
            severity_number=SeverityNumber.INFO,
        )


class InMemoryLogSink:
    """Testing/dry-run sink. Holds every record handed to it."""

    def __init__(self) -> None:
        self.records: list[dict[str, Any]] = []

    def emit_record(self, *, body: str, attributes: Mapping[str, Any]) -> None:
        self.records.append({"body": body, "attributes": dict(attributes)})


class OTelEmitter:
    """Emits the span tree and the aggregate ``red_team_result`` records."""

    def __init__(
        self,
        tracer: Tracer | None = None,
        log_sink: LogRecordSink | None = None,
    ) -> None:
        self._tracer = tracer or trace.get_tracer("fabric.redteam")
        self._log_sink = log_sink

    def emit(self, result: RunResult) -> None:
        with self._tracer.start_as_current_span(
            "fabric.redteam.run",
            kind=SpanKind.INTERNAL,
            attributes=self._run_attributes(result),
        ) as run_span:
            ctx = run_span.get_span_context()
            run_trace_id = format(ctx.trace_id, "032x")
            run_span_id = format(ctx.span_id, "016x")
            # Stamped as attributes so the aggregate log record and the
            # span tree can be joined natively. `run_id` alone would
            # correlate them only through the runner's own id space.
            run_span.set_attribute(ATTR_RUN_TRACE_ID, run_trace_id)
            run_span.set_attribute(ATTR_RUN_SPAN_ID, run_span_id)

            for probe in result.probes:
                self._emit_probe(result, probe, run_trace_id, run_span_id)
            if result.fail_count:
                run_span.set_status(
                    Status(
                        StatusCode.ERROR,
                        description=f"{result.fail_count} probe(s) failed",
                    )
                )

        self._emit_result_records(result)

    # --- spans ----------------------------------------------------------

    def _run_attributes(self, result: RunResult) -> dict[str, Any]:
        attrs: dict[str, Any] = {
            ATTR_EVENT_CLASS: "redteam_run",
            ATTR_RUN_ID: result.run_id,
            ATTR_TENANT: result.tenant_id,
            ATTR_AGENT: result.agent_id,
            ATTR_PROFILE: result.profile,
            ATTR_DURATION_MS: result.duration_ms,
            ATTR_PROBE_COUNT: len(result.probes),
            ATTR_FAIL_COUNT: result.fail_count,
            ATTR_CONTROL_IDS: list(result.controls_covered),
            ATTR_CONTROL_CATALOG_VERSION: result.catalog_version,
            ATTR_CONTROL_UNMAPPED: bool(result.unmapped_probes),
        }
        return attrs

    def _emit_probe(
        self,
        result: RunResult,
        probe: ProbeResult,
        run_trace_id: str,
        run_span_id: str,
    ) -> None:
        attrs: dict[str, Any] = {
            ATTR_EVENT_CLASS: "redteam_probe",
            ATTR_RUN_ID: result.run_id,
            ATTR_RUN_TRACE_ID: run_trace_id,
            ATTR_RUN_SPAN_ID: run_span_id,
            ATTR_TENANT: result.tenant_id,
            ATTR_AGENT: result.agent_id,
            ATTR_PROFILE: result.profile,
            ATTR_SUITE_ID: probe.suite,
            ATTR_SUITE_VERSION: probe.suite_version,
            ATTR_PROBE_ID: probe.probe,
            ATTR_VERDICT: probe.verdict.value,
            ATTR_DURATION_MS: probe.duration_ms,
            ATTR_ATTEMPTS: probe.attempts,
            ATTR_FINDINGS: len(probe.findings),
            ATTR_CONTROL_CATALOG_VERSION: result.catalog_version,
            ATTR_CONTROL_UNMAPPED: probe.control_unmapped,
        }
        # Only the singular key is conditional: an absent
        # `fabric.control.id` plus `unmapped=true` is unambiguous, while
        # an empty-string control id would pollute every join.
        if probe.control_id is not None:
            attrs[ATTR_CONTROL_ID] = probe.control_id

        with self._tracer.start_as_current_span(
            "fabric.redteam.probe",
            kind=SpanKind.INTERNAL,
            attributes=attrs,
        ) as span:
            ctx = span.get_span_context()
            span.set_attribute(ATTR_PROBE_SPAN_ID, format(ctx.span_id, "016x"))
            if probe.is_fail():
                span.set_status(Status(StatusCode.ERROR, description="probe failed"))

    # --- aggregate log records -------------------------------------------

    def _emit_result_records(self, result: RunResult) -> None:
        sink = self._log_sink
        if sink is None:
            sink = OTelLogSink()
        for attributes in build_red_team_result_records(result):
            sink.emit_record(
                body=(
                    f"redteam run {result.run_id} suite={attributes['suite_id']} "
                    f"failed={attributes['failed_probes']}/{attributes['total_probes']}"
                ),
                attributes=attributes,
            )


def build_red_team_result_records(result: RunResult) -> list[dict[str, Any]]:
    """Project a :class:`RunResult` into ``red_team_result`` records.

    One record **per (suite, suite_version)**, not one per run. The
    bridge model carries a singular ``suite_id`` / ``suite_version``, and
    a run may execute garak and pyrit together — collapsing them into one
    record would require putting two suite names in a single-valued
    field, i.e. lying in the field the auditor filters on.

    ``severity_counts`` is JSON-encoded because OTel attribute values
    cannot be maps and the collector allowlist matches the key
    ``severity_counts`` exactly; flattening to ``severity_counts.high``
    would be stripped.

    Control coverage is deliberately absent here. The bridge's
    ``RedTeamResult`` field set is mirrored field-for-field by the
    collector allowlist and by a parity test in the internal repo; adding
    a field on this side alone breaks it. The join key lives on the probe
    spans, where the prefix allowlist admits ``fabric.control.*`` with no
    coordination required."""

    grouped: dict[tuple[str, str], list[ProbeResult]] = {}
    for probe in result.probes:
        grouped.setdefault((probe.suite, probe.suite_version), []).append(probe)

    records: list[dict[str, Any]] = []
    for (suite_id, suite_version), probes in sorted(grouped.items()):
        records.append(
            {
                LOG_ATTR_EVENT_CLASS: RED_TEAM_RESULT_CLASS,
                "tenant_id": result.tenant_id,
                "agent_id": result.agent_id,
                "run_id": result.run_id,
                "suite_id": suite_id,
                "suite_version": suite_version,
                "started_at": result.started_at.isoformat(),
                "finished_at": result.finished_at.isoformat(),
                "total_probes": len(probes),
                "failed_probes": sum(1 for p in probes if p.is_fail()),
                "severity_counts": json.dumps(
                    result.severity_counts(probes), separators=(",", ":"), sort_keys=True
                ),
            }
        )
    return records


class InMemoryEmitter:
    """Testing/dry-run sink. Holds every run handed to it."""

    def __init__(self) -> None:
        self.runs: list[RunResult] = []

    def emit(self, result: RunResult) -> None:
        self.runs.append(result)


def _unused(_: Sequence[Any]) -> None:  # pragma: no cover
    """Placeholder kept out of the public surface."""
