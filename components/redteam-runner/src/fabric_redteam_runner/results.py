# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Shared result types.

Every suite (Garak, PyRIT, future additions) normalizes its output to
:class:`ProbeResult` so the emitter and scheduler don't need to know
which library produced it. This is the stable contract downstream
dashboards and judge-workers rely on — changing it is a breaking
change for every consumer.

Identifier and evidence types are copied verbatim from the canonical
contract in spec 024 §6.1/§6.3 (reference implementation: the internal
``evaluation-ingest`` component). The literal regexes are asserted by
``tests/test_identifier_contract.py`` so a drifted copy fails on the
pattern rather than on some accidental input that satisfies both."""

from __future__ import annotations

from datetime import datetime
from enum import StrEnum
from typing import Annotated

from pydantic import BaseModel, ConfigDict, Field, StringConstraints, model_validator

from .controls import ControlId

#: W3C trace id — 32 lowercase hex. Uppercase is rejected on purpose:
#: two spellings of one id are two ids as far as any join is concerned.
TraceId = Annotated[str, StringConstraints(pattern=r"^[0-9a-f]{32}$")]
#: W3C span id — 16 lowercase hex.
SpanId = Annotated[str, StringConstraints(pattern=r"^[0-9a-f]{16}$")]
#: SHA-256 digest — 64 lowercase hex.
Sha256 = Annotated[str, StringConstraints(pattern=r"^[0-9a-f]{64}$")]


class _ResultBase(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True, str_strip_whitespace=True)


class Severity(StrEnum):
    """Severity of an individual finding. Matches the OWASP LLM Top 10
    scale (info/low/medium/high/critical)."""

    INFO = "info"
    LOW = "low"
    MEDIUM = "medium"
    HIGH = "high"
    CRITICAL = "critical"


class CaptureStatus(StrEnum):
    """Whether the evidence behind a finding was actually retained
    (spec 024 §6.3). Copied verbatim from the canonical contract.

    This exists so "we have no digest" is *stated* rather than encoded
    as an empty string masquerading as one."""

    CAPTURED = "captured"
    REDACTED = "redacted"
    TRUNCATED = "truncated"
    NOT_CAPTURED = "not_captured"
    STORE_FAILED = "store_failed"


class Verdict(StrEnum):
    """Probe-level pass/fail. ``pass`` means the target behaved
    defensibly; ``fail`` means the probe successfully provoked an
    undesired output; ``error`` means the probe couldn't be run."""

    PASS = "pass"
    FAIL = "fail"
    ERROR = "error"


#: The four severities that roll up into ``RedTeamResult.severity_counts``.
#: ``info`` is deliberately excluded — the bridge model only keys
#: low/medium/high/critical, and an info-level note is not a finding.
COUNTED_SEVERITIES: tuple[Severity, ...] = (
    Severity.LOW,
    Severity.MEDIUM,
    Severity.HIGH,
    Severity.CRITICAL,
)


class Finding(_ResultBase):
    """One attack artifact — the evidence a probe generated.

    ``prompt_hash`` / ``response_hash`` are optional SHA-256 digests
    paired with an explicit :class:`CaptureStatus`. Previously they were
    unconstrained ``str`` and the error paths passed ``""`` — an empty
    string sitting in a field whose name promises a digest. Spec 024
    §6.3 requires the absence to be declared, not implied."""

    attempt_id: str
    prompt_hash: Sha256 | None = None
    response_hash: Sha256 | None = None
    capture_status: CaptureStatus = CaptureStatus.NOT_CAPTURED
    severity: Severity = Severity.LOW
    notes: str = ""

    @model_validator(mode="after")
    def _captured_has_integrity(self) -> Finding:
        if self.capture_status is CaptureStatus.CAPTURED and (
            self.prompt_hash is None or self.response_hash is None
        ):
            raise ValueError("captured evidence requires prompt_hash and response_hash")
        return self


class ProbeResult(_ResultBase):
    """One probe, one verdict. May carry zero or more Findings."""

    suite: str
    #: Version of the upstream suite library that produced this result.
    #: Part of the join key: "tested under garak 0.9.0.15" and "tested
    #: under garak 0.11" are different claims.
    suite_version: str = "unknown"
    probe: str
    verdict: Verdict
    #: Catalog-resolved defensive capability this probe tests
    #: (:mod:`.controls`). ``None`` means the probe is not in the
    #: catalog — a coverage gap, surfaced as ``fabric.control.unmapped``
    #: rather than dropped.
    control_id: ControlId | None = None
    duration_ms: int = 0
    attempts: int = 1
    findings: list[Finding] = Field(default_factory=list)

    def is_fail(self) -> bool:
        return self.verdict is Verdict.FAIL

    @property
    def control_unmapped(self) -> bool:
        return self.control_id is None


class RunResult(_ResultBase):
    """Everything a single invocation of the runner produced."""

    #: 32 lowercase hex, :data:`TraceId`-shaped. Previously
    #: ``"run-" + uuid4().hex[:12]``, which satisfied no identifier
    #: contract and could not be joined to anything.
    run_id: TraceId
    tenant_id: str
    agent_id: str
    profile: str
    started_at: datetime
    finished_at: datetime
    #: Version of the control catalog that resolved every
    #: ``ProbeResult.control_id`` in this run. Both sides of the join
    #: must agree on this or the join is invalid.
    catalog_version: str = ""
    probes: list[ProbeResult] = Field(default_factory=list)

    @property
    def fail_count(self) -> int:
        return sum(1 for p in self.probes if p.is_fail())

    @property
    def duration_ms(self) -> int:
        return int((self.finished_at - self.started_at).total_seconds() * 1000)

    @property
    def controls_covered(self) -> tuple[str, ...]:
        """Deduplicated, sorted control ids this run exercised.

        Sorted so the attribute value is stable across runs with the
        same coverage — an unsorted list would make every run look like
        a coverage change."""

        return tuple(sorted({p.control_id for p in self.probes if p.control_id is not None}))

    @property
    def unmapped_probes(self) -> tuple[str, ...]:
        """``"<suite>:<probe>"`` for every probe with no catalog entry."""

        return tuple(sorted({f"{p.suite}:{p.probe}" for p in self.probes if p.control_unmapped}))

    def severity_counts(self, probes: list[ProbeResult] | None = None) -> dict[str, int]:
        """Finding counts by severity, restricted to the four keys the
        bridge's ``RedTeamResult.severity_counts`` accepts."""

        scope = self.probes if probes is None else probes
        counts = {s.value: 0 for s in COUNTED_SEVERITIES}
        for probe in scope:
            for finding in probe.findings:
                if finding.severity in COUNTED_SEVERITIES:
                    counts[finding.severity.value] += 1
        return counts
