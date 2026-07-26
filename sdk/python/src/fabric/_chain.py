# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Internal guardrail chain that composes the configured rails into
the spec-005 ``GuardrailResult`` shape.

Current rails (in pipeline order):

1. **Presidio** — redacts PII. Never blocks.
2. **NeMo Colang** — dialog / jailbreak / refusal rails. May block and
   may fire policies, but MAY NOT rewrite content except via an
   explicit ``redact`` verdict. NeMo's Colang path returns the
   *assistant completion* in ``modified_value``, so honouring it on any
   other action would replace the caller's payload with generated text
   (defect #9). A ``block`` carries its canned refusal in
   ``block_response``; ``redacted_content`` stays the audit record of
   what was actually submitted.
3. **Extra checkers** — pluggable :class:`GuardrailChecker` tiers
   (Lakera, generic HTTP, custom classifiers), run in order after
   NeMo. Each may fire a policy, block, or escalate, and may rewrite
   content on ``redact`` / ``warn`` / ``block``. ``allow`` and
   ``escalate`` verdicts never rewrite: "allow + a different value" is
   self-contradictory.

Presidio runs first so NeMo's (potentially LLM-backed) check never
sees raw PII. Each rail is optional — the chain works with any
subset, and ``has_rails`` is true if at least one is wired.

This module is ``_chain`` (leading underscore) — not part of the
public API. Hosts configure the chain implicitly via
:class:`fabric.Fabric` and interact with it only through
:class:`fabric.Decision`.
"""

from __future__ import annotations

import logging
import time
from typing import TYPE_CHECKING
from uuid import uuid4

from .guardrails import EntitySummary, GuardrailPhase, GuardrailResult

if TYPE_CHECKING:
    from .guardrails import GuardrailChecker
    from .nemo import NemoClient
    from .presidio import PresidioClient

logger = logging.getLogger("fabric.chain")

# Actions whose ``modified_value`` is a transformation of the value we
# submitted, and may therefore replace it. Everything else is a
# *statement about* that value, not a replacement for it.
_NEMO_REWRITING_ACTIONS: frozenset[str] = frozenset({"redact"})
_CHECKER_REWRITING_ACTIONS: frozenset[str] = frozenset({"redact", "warn", "block"})


def _resolve_content(
    *,
    current: str,
    action: str,
    modified_value: str | None,
    rewriting_actions: frozenset[str],
    source: str,
    rail: str | None,
) -> str:
    """Return the content to carry forward after one rail's verdict.

    ``modified_value`` is honoured only for actions in
    ``rewriting_actions`` — those whose modified value is a
    *transformation of the value we submitted*. Every other action is a
    statement *about* that value, so its payload is ignored and a
    contract violation is logged.

    The non-empty guard is retained for the rewriting actions so a
    verdict with no payload cannot destroy an upstream rail's output.

    The warning deliberately names only the rail and action — logging
    the value would spill model output / user content into application
    logs.
    """

    if action in rewriting_actions:
        return modified_value or current
    if modified_value and modified_value != current:
        logger.warning(
            "%s %r returned action=%r with a differing modified_value; ignoring it "
            "(allowed rewriting actions: %s)",
            source,
            rail,
            action,
            ", ".join(sorted(rewriting_actions)),
        )
    return current


class GuardrailChain:
    """Applies the configured rails to a single phase of a decision."""

    def __init__(
        self,
        *,
        presidio: PresidioClient | None = None,
        nemo: NemoClient | None = None,
        extra_checkers: list[GuardrailChecker] | None = None,
    ) -> None:
        self._presidio = presidio
        self._nemo = nemo
        self._extra_checkers: list[GuardrailChecker] = list(extra_checkers or [])

    @property
    def has_rails(self) -> bool:
        return self._presidio is not None or self._nemo is not None or bool(self._extra_checkers)

    def check(self, *, phase: GuardrailPhase, path: str, value: str) -> GuardrailResult:
        start = time.monotonic()
        entities: list[EntitySummary] = []
        policies: list[str] = []
        content = value
        blocked = False
        block_response: str | None = None

        if self._presidio is not None:
            presidio_result = self._presidio.redact(path, value)
            content = presidio_result.value
            # Record the detected entity whenever PII was redacted — in
            # HMAC mode that is signalled by ``hashed``; in tag mode the
            # value is rewritten in place (``hashed=False``) but a
            # ``pii_category`` is still returned. Gating only on ``hashed``
            # made tag-mode redactions invisible in the audit trail.
            if presidio_result.hashed or presidio_result.pii_category:
                entities.append(EntitySummary(category=presidio_result.pii_category, count=1))
                policies.append(f"presidio:{presidio_result.pii_category}")

        if self._nemo is not None:
            nemo_result = self._nemo.check(phase, path, content)
            # Only an explicit ``redact`` may rewrite the content. NeMo's
            # Colang dialog path returns the *assistant completion* in
            # ``modified_value`` for every non-redact action, so trusting
            # it on "allow" silently replaced the user's prompt with a
            # generated reply (defect #9). A "block" carries its canned
            # refusal in ``block_response``; ``redacted_content`` must
            # stay the audit record of what was actually submitted.
            content = _resolve_content(
                current=content,
                action=nemo_result.action,
                modified_value=nemo_result.modified_value,
                rewriting_actions=_NEMO_REWRITING_ACTIONS,
                source="nemo rail",
                rail=nemo_result.rail,
            )
            if nemo_result.action != "allow":
                # Record only the rail in policies_fired. EntitySummary
                # represents PII *entity classes* (e.g. EMAIL, PERSON);
                # mixing rail names ("jailbreak_defence") into the same
                # list confuses downstream consumers that aggregate by
                # entity category.
                policies.append(f"nemo:{nemo_result.rail}")
            if nemo_result.action == "block":
                blocked = True
                block_response = nemo_result.block_response

        # Pluggable checker tiers run in order after NeMo. Each may fire
        # a policy, block, escalate, or rewrite content (redact / warn /
        # block only — see _CHECKER_REWRITING_ACTIONS). A raised
        # exception fails closed (the checker is converted to a block).
        # The loop short-circuits on the first block so a downstream
        # checker can't un-block an upstream one.
        if not blocked:
            for checker in self._extra_checkers:
                try:
                    verdict = checker.check(phase, path, content)
                except Exception as exc:  # fail-closed: any error becomes a block
                    blocked = True
                    block_response = f"{checker.name} raised: {exc}"
                    policies.append(f"{checker.name}:{checker.name}")
                    break

                # Same rule as the NeMo tier, with a wider mutable set:
                # a checker verdict is host-authored, and warn+rewrite /
                # block+substitution are deliberate features. But
                # "allow" and "escalate" assert the value is fine as-is,
                # so they may not replace it.
                content = _resolve_content(
                    current=content,
                    action=verdict.action,
                    modified_value=verdict.modified_value,
                    rewriting_actions=_CHECKER_REWRITING_ACTIONS,
                    source="checker",
                    rail=verdict.rail or checker.name,
                )
                if verdict.action != "allow":
                    policies.append(f"{checker.name}:{verdict.rail or checker.name}")
                if verdict.action == "block":
                    blocked = True
                    block_response = verdict.reason
                    break
                # ``escalate`` is surfaced via policies_fired (recorded
                # above); the host decides what to do. It does not block.

        latency_ms = (time.monotonic() - start) * 1000.0
        return GuardrailResult(
            event_id=uuid4(),
            blocked=blocked,
            block_response=block_response,
            redacted_content=content,
            entities_detected=entities,
            policies_fired=policies,
            latency_ms=latency_ms,
        )

    def close(self) -> None:
        if self._presidio is not None:
            self._presidio.close()
        if self._nemo is not None:
            self._nemo.close()
        for checker in self._extra_checkers:
            checker.close()
