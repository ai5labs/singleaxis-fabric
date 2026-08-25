# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Core rails-check logic.

The sidecar's one job: given a ``(phase, path, value)`` tuple, run it
through a Colang rails runner and return an action. The engine
interface is pluggable — NeMo in production, a passthrough engine in
tests and in setups where the operator has not wired any rails yet.

The wire contract is fixed by ``sdk/python/src/fabric/nemo.py``; if
you are changing these models you are also changing the SDK.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from pathlib import Path
from typing import Literal, Protocol

from pydantic import BaseModel, ConfigDict, Field

from fabric_nemo_sidecar.literal_filter import LiteralJailbreakFilter

_LOG = logging.getLogger("fabric_nemo_sidecar")

CheckAction = Literal["allow", "redact", "block", "warn"]


class CheckRequest(BaseModel):
    """Input to ``POST /v1/check``."""

    model_config = ConfigDict(extra="forbid", frozen=True, str_strip_whitespace=False)

    phase: Literal["input", "output_stream", "output_final"]
    path: str = Field(min_length=1, max_length=256)
    value: str = Field(min_length=0, max_length=64_000)


class CheckResponse(BaseModel):
    """Output from ``POST /v1/check``."""

    model_config = ConfigDict(extra="forbid", frozen=True)

    allowed: bool
    action: CheckAction
    rail: str
    block_response: str | None = None
    modified_value: str


@dataclass(slots=True)
class EngineResult:
    """Internal result from a ``RailsEngine`` implementation."""

    allowed: bool
    action: CheckAction
    rail: str
    block_response: str | None
    modified_value: str


class RailsEngine(Protocol):
    """Pluggable rails backend.

    The NeMo adapter implements this over ``LLMRails``; the passthrough
    engine is the safe default when no Colang config is loaded.
    """

    def check(self, phase: str, path: str, value: str) -> EngineResult: ...


class PassthroughEngine:
    """Engine that allows everything. Used as a safe default in tests
    and when ``nemoguardrails`` is not installed or no config path is
    configured. ``rail`` is fixed so span events remain queryable."""

    __slots__ = ("_rail",)

    def __init__(self, rail: str = "passthrough") -> None:
        self._rail = rail

    def check(self, phase: str, path: str, value: str) -> EngineResult:
        return EngineResult(
            allowed=True,
            action="allow",
            rail=self._rail,
            block_response=None,
            modified_value=value,
        )


_STARTER_CONFIG_LINES = (
    "models: []",
    "rails:",
    "input:",
    "flows: []",
    "output:",
    "flows: []",
)


def _significant_lines(path: Path) -> tuple[str, ...]:
    """Return non-empty, non-comment lines with indentation removed."""

    return tuple(
        line.strip()
        for line in path.read_text(encoding="utf-8").splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    )


def validate_deterministic_starter_bundle(config_path: str | Path) -> None:
    """Fail unless *config_path* declares the model-free starter bundle.

    The deterministic starter does not execute Colang. Requiring the exact
    no-model/no-flow declaration prevents an operator from accidentally
    selecting literal-only mode for a custom bundle and believing its Colang
    flows are active.
    """

    bundle = Path(config_path)
    config = bundle / "config.yml"
    rails = bundle / "rails.co"
    if not config.is_file() or not rails.is_file():
        raise ValueError(f"deterministic starter requires config.yml and rails.co in {bundle}")
    if _significant_lines(config) != _STARTER_CONFIG_LINES:
        raise ValueError(
            "deterministic starter config.yml must declare models: [] and "
            "empty input/output flows only; use NeMo mode for custom rails"
        )
    if _significant_lines(rails):
        raise ValueError(
            "deterministic starter rails.co must contain no active Colang; "
            "use NeMo mode for custom rails"
        )


class DeterministicStarterEngine:
    """Literal-only starter that never initializes or calls NeMo.

    Input-phase values matching the configured literal filter are blocked.
    Every other value and every output-phase value is allowed byte-for-byte.
    This intentionally small behavior is deterministic, credential-free, and
    incapable of external I/O.
    """

    __slots__ = ("_literal_filter",)

    def __init__(self, literal_filter: LiteralJailbreakFilter) -> None:
        self._literal_filter = literal_filter

    @property
    def literal_filter(self) -> LiteralJailbreakFilter:
        return self._literal_filter

    def check(self, phase: str, path: str, value: str) -> EngineResult:
        del path
        match = self._literal_filter.check(value) if phase == "input" else None
        if match is not None:
            return EngineResult(
                allowed=False,
                action="block",
                rail=match.rail,
                block_response=match.block_response,
                modified_value=value,
            )
        return EngineResult(
            allowed=True,
            action="allow",
            rail="deterministic_starter",
            block_response=None,
            modified_value=value,
        )


class RailsChecker:
    """Applies a :class:`RailsEngine` and returns a wire-level
    :class:`CheckResponse`. The pydantic boundary lives here so
    engines can stay free of FastAPI / pydantic coupling.

    This is also where the ``modified_value`` invariant is enforced, and
    it is enforced here rather than in the NeMo adapter because this is
    the only layer that sees *both* the request and the engine result.
    Operators may plug in any :class:`RailsEngine`; a third-party or
    in-house engine must not be able to put arbitrary content on the
    wire under an ``allow`` verdict.

    The invariant: ``modified_value`` is a transformation of the
    submitted value and MUST equal it byte-for-byte unless the action is
    ``redact``. An assistant completion is not a modified value. This
    exists because NeMo's Colang path returns ``LLMRails.generate()``
    output — a chat completion — and returning that as the "modified"
    input silently replaced the caller's message with chatbot text under
    ``allowed=true``.
    """

    __slots__ = ("_engine",)

    def __init__(self, engine: RailsEngine) -> None:
        self._engine = engine

    def check(self, request: CheckRequest) -> CheckResponse:
        result = self._engine.check(request.phase, request.path, request.value)
        modified_value = result.modified_value
        if result.action != "redact" and modified_value != request.value:
            # Fail loud, not silent: an engine that rewrites content
            # outside a redact action is misbehaving, and swallowing it
            # would hide the bug this guard exists to catch.
            _LOG.warning(
                "rails engine returned a modified_value under action=%r on rail %r; "
                "only 'redact' may rewrite content. Substituting the submitted "
                "value. Engine=%s",
                result.action,
                result.rail,
                type(self._engine).__name__,
            )
            modified_value = request.value
        return CheckResponse(
            allowed=result.allowed,
            action=result.action,
            rail=result.rail,
            block_response=result.block_response,
            modified_value=modified_value,
        )
