# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""End-to-end Decision → GuardrailChain → fake {Presidio,NeMo} wiring."""

from __future__ import annotations

import pytest
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

from fabric import (
    CheckerVerdict,
    Fabric,
    FabricConfig,
    GuardrailBlocked,
    GuardrailChecker,
)
from fabric.guardrails import GuardrailAction
from fabric.nemo import NemoClient, NemoResult
from fabric.presidio import PresidioClient, RedactionResult


class _FakePresidio:
    """Minimal :class:`PresidioClient` stand-in for SDK-side tests."""

    def __init__(self, result: RedactionResult) -> None:
        self.result = result
        self.calls: list[tuple[str, str]] = []

    def redact(self, path: str, value: str) -> RedactionResult:
        self.calls.append((path, value))
        return self.result

    def close(self) -> None:
        pass


class _FakeNemo:
    """Minimal :class:`NemoClient` stand-in for SDK-side tests."""

    def __init__(self, result: NemoResult) -> None:
        self.result = result
        self.calls: list[tuple[str, str, str]] = []
        self.closed = 0

    def check(self, phase: str, path: str, value: str) -> NemoResult:
        self.calls.append((phase, path, value))
        return self.result

    def close(self) -> None:
        self.closed += 1


def _client(
    presidio: PresidioClient | None = None,
    nemo: NemoClient | None = None,
) -> Fabric:
    return Fabric(
        FabricConfig(tenant_id="acme", agent_id="bot"),
        presidio=presidio,
        nemo=nemo,
    )


def test_guard_input_returns_redacted_value_and_records_event(
    span_exporter: InMemorySpanExporter,
) -> None:
    fake = _FakePresidio(RedactionResult(value="[REDACTED]", hashed=True, pii_category="EMAIL"))
    fabric = _client(fake)
    with fabric.decision(session_id="s", request_id="r") as dec:
        out = dec.guard_input("email me at a@b.com")
    assert out == "[REDACTED]"
    assert fake.calls == [("input", "email me at a@b.com")]

    span = span_exporter.get_finished_spans()[0]
    events = [ev for ev in span.events if ev.name == "fabric.guardrail"]
    assert len(events) == 1
    attrs = dict(events[0].attributes or {})
    assert attrs["fabric.guardrail.phase"] == "input"
    assert attrs["fabric.guardrail.policies"] == ("presidio:EMAIL",)
    assert attrs["fabric.guardrail.entities"] == ("EMAIL:1",)
    assert attrs["fabric.guardrail.blocked"] is False


def test_tag_mode_redaction_records_entity_on_span(
    span_exporter: InMemorySpanExporter,
) -> None:
    """Tag-mode redaction (``hashed=False`` with a rewritten value) must
    still record the detected entity/policy on the guardrail event — the
    audit trail has to show that PII was redacted regardless of mode."""

    fake = _FakePresidio(
        RedactionResult(value="email me at <EMAIL_1>", hashed=False, pii_category="EMAIL")
    )
    fabric = _client(fake)
    with fabric.decision(session_id="s", request_id="r") as dec:
        out = dec.guard_input("email me at a@b.com")
    assert out == "email me at <EMAIL_1>"

    span = span_exporter.get_finished_spans()[0]
    events = [ev for ev in span.events if ev.name == "fabric.guardrail"]
    assert len(events) == 1
    attrs = dict(events[0].attributes or {})
    assert attrs["fabric.guardrail.policies"] == ("presidio:EMAIL",)
    assert attrs["fabric.guardrail.entities"] == ("EMAIL:1",)


def test_guard_input_passthrough_when_sidecar_says_no_pii(
    span_exporter: InMemorySpanExporter,
) -> None:
    fake = _FakePresidio(RedactionResult(value="hello", hashed=False, pii_category=""))
    fabric = _client(fake)
    with fabric.decision(session_id="s", request_id="r") as dec:
        assert dec.guard_input("hello") == "hello"

    span = span_exporter.get_finished_spans()[0]
    events = [ev for ev in span.events if ev.name == "fabric.guardrail"]
    attrs = dict(events[0].attributes or {})
    assert "fabric.guardrail.policies" not in attrs
    assert "fabric.guardrail.entities" not in attrs


def test_guard_output_chunk_and_final_use_distinct_paths() -> None:
    fake = _FakePresidio(RedactionResult(value="x", hashed=False, pii_category=""))
    fabric = _client(fake)
    with fabric.decision(session_id="s", request_id="r") as dec:
        dec.guard_output_chunk("chunk-1")
        dec.guard_output_final("full-text")
    paths = [c[0] for c in fake.calls]
    assert paths == ["output_chunk", "output_final"]


def test_from_env_wires_presidio_socket(monkeypatch: object) -> None:
    # Client construction validates the socket path but does not probe,
    # so we can assert the plumbing without running a sidecar.
    fabric = Fabric.from_env(
        env={
            "FABRIC_TENANT_ID": "acme",
            "FABRIC_AGENT_ID": "bot",
            "FABRIC_PRESIDIO_UNIX_SOCKET": "/tmp/presidio.sock",
            "FABRIC_PRESIDIO_TIMEOUT_SECONDS": "1.5",
        }
    )
    assert fabric.guardrail_chain.has_rails is True


def test_from_env_without_socket_leaves_chain_empty() -> None:
    fabric = Fabric.from_env(env={"FABRIC_TENANT_ID": "acme", "FABRIC_AGENT_ID": "bot"})
    assert fabric.guardrail_chain.has_rails is False


def test_from_env_rejects_non_numeric_timeout() -> None:
    with pytest.raises(ValueError, match="must be a float"):
        Fabric.from_env(
            env={
                "FABRIC_TENANT_ID": "acme",
                "FABRIC_AGENT_ID": "bot",
                "FABRIC_PRESIDIO_UNIX_SOCKET": "/tmp/x",
                "FABRIC_PRESIDIO_TIMEOUT_SECONDS": "not-a-number",
            }
        )


def test_fabric_close_delegates_to_chain() -> None:
    calls = {"closed": 0}

    class _Client:
        def redact(self, path: str, value: str) -> RedactionResult:
            return RedactionResult(value=value, hashed=False, pii_category="")

        def close(self) -> None:
            calls["closed"] += 1

    fabric = Fabric(FabricConfig(tenant_id="t", agent_id="a"), presidio=_Client())
    fabric.close()
    assert calls["closed"] == 1


# -- NeMo rail --------------------------------------------------------


def test_nemo_block_propagates_to_guardrail_result(
    span_exporter: InMemorySpanExporter,
) -> None:
    fake = _FakeNemo(
        NemoResult(
            allowed=False,
            action="block",
            rail="jailbreak_defence",
            block_response="refused",
            modified_value="",
        )
    )
    fabric = _client(nemo=fake)
    with (
        fabric.decision(session_id="s", request_id="r") as dec,
        pytest.raises(GuardrailBlocked) as excinfo,
    ):
        result = fabric.guardrail_chain.check(phase="input", path="input", value="x")
        assert result.blocked is True
        assert result.block_response == "refused"
        dec.record_block(result)
        dec.raise_for_block()
    assert excinfo.value.result.policies_fired == ["nemo:jailbreak_defence"]

    span = span_exporter.get_finished_spans()[0]
    assert dict(span.attributes or {})["fabric.blocked"] is True


def test_chain_runs_presidio_before_nemo() -> None:
    """PII must be redacted before NeMo sees the value — NeMo may
    call an LLM internally, and that LLM must not see raw PII."""

    presidio = _FakePresidio(
        RedactionResult(value="email me at [REDACTED]", hashed=True, pii_category="EMAIL")
    )
    nemo = _FakeNemo(
        NemoResult(
            allowed=True,
            action="allow",
            rail="on_topic",
            block_response=None,
            modified_value="email me at [REDACTED]",
        )
    )
    fabric = _client(presidio=presidio, nemo=nemo)
    with fabric.decision(session_id="s", request_id="r") as dec:
        out = dec.guard_input("email me at a@b.com")
    assert out == "email me at [REDACTED]"
    # NeMo saw the Presidio-redacted value, not the raw one.
    assert nemo.calls == [("input", "input", "email me at [REDACTED]")]


def test_nemo_warn_records_policy_without_blocking(
    span_exporter: InMemorySpanExporter,
) -> None:
    """``warn`` is a *flag*, not a rewrite — the policy must be recorded
    while the caller's text survives byte-for-byte.

    The pre-fix assertion (``out == "rewritten"``) is exactly what let a
    generated reply overwrite the user's prompt (defect #9): NeMo's
    Colang path puts the assistant completion in ``modified_value`` for
    every non-redact action. This is the stronger assertion — the policy
    signal is unchanged, and the payload is now provably untouched.
    """

    fake = _FakeNemo(
        NemoResult(
            allowed=True,
            action="warn",
            rail="off_topic",
            block_response=None,
            modified_value="rewritten",
        )
    )
    fabric = _client(nemo=fake)
    with fabric.decision(session_id="s", request_id="r") as dec:
        out = dec.guard_input("baseball chat")
    assert out == "baseball chat"

    span = span_exporter.get_finished_spans()[0]
    events = [ev for ev in span.events if ev.name == "fabric.guardrail"]
    attrs = dict(events[0].attributes or {})
    assert attrs["fabric.guardrail.policies"] == ("nemo:off_topic",)
    assert attrs["fabric.guardrail.blocked"] is False


def test_from_env_wires_nemo_socket() -> None:
    fabric = Fabric.from_env(
        env={
            "FABRIC_TENANT_ID": "acme",
            "FABRIC_AGENT_ID": "bot",
            "FABRIC_NEMO_UNIX_SOCKET": "/tmp/nemo.sock",
            "FABRIC_NEMO_TIMEOUT_SECONDS": "2.0",
        }
    )
    assert fabric.guardrail_chain.has_rails is True


def test_from_env_rejects_non_numeric_nemo_timeout() -> None:
    with pytest.raises(ValueError, match="must be a float"):
        Fabric.from_env(
            env={
                "FABRIC_TENANT_ID": "acme",
                "FABRIC_AGENT_ID": "bot",
                "FABRIC_NEMO_UNIX_SOCKET": "/tmp/x",
                "FABRIC_NEMO_TIMEOUT_SECONDS": "nope",
            }
        )


def test_close_delegates_to_both_rails() -> None:
    nemo = _FakeNemo(
        NemoResult(allowed=True, action="allow", rail="ok", block_response=None, modified_value="")
    )
    presidio_calls = {"closed": 0}

    class _Presidio:
        def redact(self, path: str, value: str) -> RedactionResult:
            return RedactionResult(value=value, hashed=False, pii_category="")

        def close(self) -> None:
            presidio_calls["closed"] += 1

    fabric = Fabric(
        FabricConfig(tenant_id="t", agent_id="a"),
        presidio=_Presidio(),
        nemo=nemo,
    )
    fabric.close()
    assert presidio_calls["closed"] == 1
    assert nemo.closed == 1


def test_empty_nemo_modified_value_does_not_destroy_presidio_redaction(
    span_exporter: InMemorySpanExporter,
) -> None:
    """A NeMo rail that stops without canned content emits
    ``modified_value=""``. Before the fix, the chain blindly assigned
    that empty string to ``content`` and callers observed
    ``redacted_content==""`` with no signal that PII had been seen.

    After the fix: the chain treats an empty NeMo rewrite as "no
    rewrite" and keeps Presidio's redacted content intact. The block
    is still surfaced via ``blocked=True`` and ``policies_fired``.
    """

    presidio = _FakePresidio(
        RedactionResult(value="[REDACTED:EMAIL]", hashed=True, pii_category="EMAIL")
    )
    nemo = _FakeNemo(
        NemoResult(
            allowed=False,
            action="block",
            rail="jailbreak defence",
            block_response=None,
            modified_value="",
        )
    )
    fabric = _client(presidio=presidio, nemo=nemo)
    try:
        result = fabric.guardrail_chain.check(
            phase="input", path="input", value="contact me at bryan@example.com"
        )
    finally:
        fabric.close()
    assert result.blocked is True
    # Presidio's redacted content survives — chain did NOT silently
    # overwrite it with NeMo's empty modified_value.
    assert result.redacted_content == "[REDACTED:EMAIL]"
    assert "nemo:jailbreak defence" in result.policies_fired
    assert "presidio:EMAIL" in result.policies_fired


def test_non_block_nemo_with_empty_modified_value_keeps_presidio_redaction(
    span_exporter: InMemorySpanExporter,
) -> None:
    """Same defensive rule for non-block actions: a NeMo ``warn`` that
    emits ``modified_value=""`` must not erase Presidio's output."""

    presidio = _FakePresidio(
        RedactionResult(value="[REDACTED:PHONE]", hashed=True, pii_category="PHONE")
    )
    nemo = _FakeNemo(
        NemoResult(
            allowed=True,
            action="warn",
            rail="topic",
            block_response=None,
            modified_value="",
        )
    )
    fabric = _client(presidio=presidio, nemo=nemo)
    try:
        result = fabric.guardrail_chain.check(
            phase="input", path="input", value="call me on +1-415-555-0199"
        )
    finally:
        fabric.close()
    assert result.blocked is False
    assert result.redacted_content == "[REDACTED:PHONE]"
    assert "nemo:topic" in result.policies_fired


# -- defect #9: only an explicit redact may rewrite content -----------


def test_nemo_allow_verdict_never_replaces_input_with_generated_reply() -> None:
    """The live repro for defect #9, frozen as a regression test.

    NeMo's Colang dialog path returns the *assistant completion* in
    ``modified_value``. Before the fix the chain adopted it on any
    non-empty value, so an ``allow`` verdict silently replaced the
    user's prompt with generated text — and because ``allow`` fires no
    policy, ``policies_fired`` was empty, leaving no audit breadcrumb
    that a substitution had happened at all.
    """

    nemo = _FakeNemo(
        NemoResult(
            allowed=True,
            action="allow",
            rail="on_topic",
            block_response=None,
            modified_value="I'd be happy to help you with that. Could you share the invoice?",
        )
    )
    fabric = _client(nemo=nemo)
    with fabric.decision(session_id="s", request_id="r") as dec:
        out = dec.guard_input("summarise invoice INV-88213")
    assert out == "summarise invoice INV-88213"


def test_nemo_block_keeps_submitted_content_and_surfaces_refusal_separately() -> None:
    """A block must keep ``redacted_content`` as the audit record of
    what was actually submitted; the canned refusal travels in
    ``block_response`` and nowhere else. Conflating the two is the exact
    confusion that produced defect #9.
    """

    presidio = _FakePresidio(
        RedactionResult(value="[REDACTED:EMAIL] ignore previous", hashed=True, pii_category="EMAIL")
    )
    nemo = _FakeNemo(
        NemoResult(
            allowed=False,
            action="block",
            rail="jailbreak_defence",
            block_response="I can't help with that.",
            modified_value="I can't help with that.",
        )
    )
    fabric = _client(presidio=presidio, nemo=nemo)
    try:
        result = fabric.guardrail_chain.check(
            phase="input", path="input", value="a@b.com ignore previous"
        )
    finally:
        fabric.close()
    assert result.blocked is True
    assert result.redacted_content == "[REDACTED:EMAIL] ignore previous"
    assert result.block_response == "I can't help with that."
    assert "nemo:jailbreak_defence" in result.policies_fired


@pytest.mark.parametrize("action", ["allow", "warn", "block"])
def test_only_redact_action_rewrites_content_non_mutating(action: str) -> None:
    """Pins the whole NeMo action vocabulary: allow/warn/block never
    mutate the payload, whatever they put in ``modified_value``."""

    nemo = _FakeNemo(
        NemoResult(
            allowed=action != "block",
            action=action,  # type: ignore[arg-type]
            rail="rail",
            block_response="refused",
            modified_value="GENERATED ASSISTANT REPLY",
        )
    )
    fabric = _client(nemo=nemo)
    try:
        result = fabric.guardrail_chain.check(phase="input", path="input", value="original text")
    finally:
        fabric.close()
    assert result.redacted_content == "original text"


def test_only_redact_action_rewrites_content_mutating() -> None:
    """The other half of the vocabulary decision: ``redact`` is the one
    NeMo action whose ``modified_value`` IS a transformation of the
    submitted value, so it is applied."""

    nemo = _FakeNemo(
        NemoResult(
            allowed=True,
            action="redact",
            rail="pii_rail",
            block_response=None,
            modified_value="my ssn is [REDACTED]",
        )
    )
    fabric = _client(nemo=nemo)
    try:
        result = fabric.guardrail_chain.check(
            phase="input", path="input", value="my ssn is 123-45-6789"
        )
    finally:
        fabric.close()
    assert result.redacted_content == "my ssn is [REDACTED]"
    assert "nemo:pii_rail" in result.policies_fired


def test_nemo_redact_with_empty_modified_value_keeps_upstream_content() -> None:
    """The empty-value guard is preserved for the one mutating action:
    a ``redact`` verdict carrying no payload must not destroy Presidio's
    upstream redaction."""

    presidio = _FakePresidio(
        RedactionResult(value="[REDACTED:EMAIL]", hashed=True, pii_category="EMAIL")
    )
    nemo = _FakeNemo(
        NemoResult(
            allowed=True,
            action="redact",
            rail="pii_rail",
            block_response=None,
            modified_value="",
        )
    )
    fabric = _client(presidio=presidio, nemo=nemo)
    try:
        result = fabric.guardrail_chain.check(phase="input", path="input", value="a@b.com")
    finally:
        fabric.close()
    assert result.redacted_content == "[REDACTED:EMAIL]"


def test_chain_warns_when_nemo_violates_the_rewrite_contract(
    caplog: pytest.LogCaptureFixture,
) -> None:
    """A contract violation is ignored *and observable*: the chain logs
    a WARNING naming the rail and action. The log record must NOT carry
    the value itself — that would spill model output / user content into
    application logs.
    """

    submitted = "summarise invoice INV-88213"
    reply = "I'd be happy to help you with that."
    nemo = _FakeNemo(
        NemoResult(
            allowed=True,
            action="allow",
            rail="on_topic",
            block_response=None,
            modified_value=reply,
        )
    )
    fabric = _client(nemo=nemo)
    with caplog.at_level("WARNING", logger="fabric.chain"):
        try:
            result = fabric.guardrail_chain.check(phase="input", path="input", value=submitted)
        finally:
            fabric.close()

    assert result.redacted_content == submitted
    records = [r for r in caplog.records if r.name == "fabric.chain"]
    assert len(records) == 1
    message = records[0].getMessage()
    assert "on_topic" in message
    assert "allow" in message
    assert submitted not in message
    assert reply not in message


# -- pluggable GuardrailChecker tier ----------------------------------


class _FakeChecker:
    """Minimal :class:`GuardrailChecker` stand-in for SDK-side tests."""

    def __init__(
        self,
        name: str,
        verdict: CheckerVerdict | None = None,
        *,
        raises: Exception | None = None,
    ) -> None:
        self.name = name
        self._verdict = verdict or CheckerVerdict(action="allow")
        self._raises = raises
        self.calls: list[tuple[str, str, str]] = []
        self.closed = 0

    def check(self, phase: str, path: str, value: str) -> CheckerVerdict:
        self.calls.append((phase, path, value))
        if self._raises is not None:
            raise self._raises
        return self._verdict

    def close(self) -> None:
        self.closed += 1


def test_extra_checker_runs_after_presidio_and_nemo() -> None:
    presidio = _FakePresidio(RedactionResult(value="[REDACTED]", hashed=True, pii_category="EMAIL"))
    nemo = _FakeNemo(
        NemoResult(
            allowed=True,
            action="allow",
            rail="ok",
            block_response=None,
            modified_value="[REDACTED]",
        )
    )
    checker = _FakeChecker("lakera", CheckerVerdict(action="allow"))
    fabric = Fabric(
        FabricConfig(tenant_id="t", agent_id="a"),
        presidio=presidio,
        nemo=nemo,
        guardrail_checkers=[checker],
    )
    try:
        fabric.guardrail_chain.check(phase="input", path="input", value="email a@b.com")
    finally:
        fabric.close()
    # Checker saw the Presidio+NeMo-processed content, not the raw value.
    assert checker.calls == [("input", "input", "[REDACTED]")]


def test_extra_checker_block_short_circuits_remaining_checkers() -> None:
    first = _FakeChecker(
        "blocker",
        CheckerVerdict(action="block", reason="policy violation", rail="toxicity"),
    )
    second = _FakeChecker("never", CheckerVerdict(action="allow"))
    fabric = Fabric(
        FabricConfig(tenant_id="t", agent_id="a"),
        guardrail_checkers=[first, second],
    )
    try:
        result = fabric.guardrail_chain.check(phase="input", path="input", value="x")
    finally:
        fabric.close()
    assert result.blocked is True
    assert result.block_response == "policy violation"
    assert "blocker:toxicity" in result.policies_fired
    # Second checker must not run after the first one blocks.
    assert second.calls == []


def test_extra_checker_exception_fails_closed() -> None:
    boom = _FakeChecker("flaky", raises=RuntimeError("upstream down"))
    fabric = Fabric(
        FabricConfig(tenant_id="t", agent_id="a"),
        guardrail_checkers=[boom],
    )
    try:
        result = fabric.guardrail_chain.check(phase="input", path="input", value="x")
    finally:
        fabric.close()
    assert result.blocked is True
    assert result.block_response is not None
    assert "flaky raised" in result.block_response
    assert "upstream down" in result.block_response


def test_extra_checker_applies_modified_value_and_warn_policy() -> None:
    checker = _FakeChecker(
        "rewriter",
        CheckerVerdict(action="warn", modified_value="cleaned", rail="pii_followup"),
    )
    fabric = Fabric(
        FabricConfig(tenant_id="t", agent_id="a"),
        guardrail_checkers=[checker],
    )
    try:
        result = fabric.guardrail_chain.check(phase="input", path="input", value="dirty")
    finally:
        fabric.close()
    assert result.blocked is False
    assert result.redacted_content == "cleaned"
    assert "rewriter:pii_followup" in result.policies_fired


def test_extra_checker_escalate_records_policy_without_blocking() -> None:
    checker = _FakeChecker(
        "escalator",
        CheckerVerdict(action="escalate", rail="needs_review"),
    )
    fabric = Fabric(
        FabricConfig(tenant_id="t", agent_id="a"),
        guardrail_checkers=[checker],
    )
    try:
        result = fabric.guardrail_chain.check(phase="input", path="input", value="x")
    finally:
        fabric.close()
    assert result.blocked is False
    assert "escalator:needs_review" in result.policies_fired


def test_extra_checker_allow_verdict_cannot_mutate_content() -> None:
    """ "allow + a different value" is self-contradictory: the checker
    asserted the value it was handed is fine, so it may not hand back a
    different one. Same rule as the NeMo tier (defect #9)."""

    checker = _FakeChecker(
        "rogue",
        CheckerVerdict(action="allow", modified_value="something else entirely"),
    )
    fabric = Fabric(
        FabricConfig(tenant_id="t", agent_id="a"),
        guardrail_checkers=[checker],
    )
    try:
        result = fabric.guardrail_chain.check(phase="input", path="input", value="original")
    finally:
        fabric.close()
    assert result.blocked is False
    assert result.redacted_content == "original"
    assert result.policies_fired == []


def test_extra_checker_escalate_verdict_cannot_mutate_content() -> None:
    """``escalate`` defers to a human reviewer; the reviewer must see
    what was actually submitted, so the payload is never rewritten."""

    checker = _FakeChecker(
        "escalator",
        CheckerVerdict(action="escalate", modified_value="rewritten", rail="needs_review"),
    )
    fabric = Fabric(
        FabricConfig(tenant_id="t", agent_id="a"),
        guardrail_checkers=[checker],
    )
    try:
        result = fabric.guardrail_chain.check(phase="input", path="input", value="original")
    finally:
        fabric.close()
    assert result.blocked is False
    assert result.redacted_content == "original"
    assert "escalator:needs_review" in result.policies_fired


def test_has_rails_true_when_only_extra_checker_wired() -> None:
    checker = _FakeChecker("solo")
    fabric = Fabric(
        FabricConfig(tenant_id="t", agent_id="a"),
        guardrail_checkers=[checker],
    )
    assert fabric.guardrail_chain.has_rails is True


def test_close_delegates_to_extra_checkers() -> None:
    checker = _FakeChecker("closeme")
    fabric = Fabric(
        FabricConfig(tenant_id="t", agent_id="a"),
        guardrail_checkers=[checker],
    )
    fabric.close()
    assert checker.closed == 1


def test_fake_checker_satisfies_runtime_checkable_protocol() -> None:
    assert isinstance(_FakeChecker("p"), GuardrailChecker)


def test_checker_verdict_default_action_field() -> None:
    verdict = CheckerVerdict(action=cast_action("allow"))
    assert verdict.modified_value is None
    assert verdict.reason is None
    assert verdict.rail is None


def cast_action(value: str) -> GuardrailAction:
    """Narrow a str literal to GuardrailAction for the typed test above."""
    assert value in ("allow", "redact", "warn", "block", "escalate")
    return value  # type: ignore[return-value]
