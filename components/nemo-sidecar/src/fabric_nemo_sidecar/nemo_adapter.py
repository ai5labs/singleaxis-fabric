# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Adapter that wires NeMo Guardrails' ``LLMRails`` to the sidecar's
:class:`RailsEngine` protocol.

Import is lazy and guarded so the sidecar can be installed and tested
without ``nemoguardrails`` and its transformer / LLM dependency chain.

The adapter accepts two response shapes:

* **Modern** (``nemoguardrails`` ≥ 0.10): ``LLMRails.generate(...,
  options={"log": {"activated_rails": True}})`` returns a
  ``GenerationResponse`` whose ``log.activated_rails`` list reports the
  rails that fired, each carrying a ``stop`` flag and a ``decisions``
  list. Any input / dialog / output rail with ``stop == True`` (or
  ``"stop"`` in its decisions) is treated as a block.
* **Legacy** (pre-0.10 dict-style stubs): ``response["rails_info"]``
  carries ``rail``, ``action`` and ``block_response`` keys directly.
  Retained for backward compatibility with adapter fakes and older
  ``nemoguardrails`` builds that still emit the flat shape.

Pydantic ``GenerationResponse`` objects and plain ``dict`` payloads are
both accepted — every accessor goes through :func:`_get` which tries
``__getitem__`` then ``getattr`` so the adapter does not couple to a
specific ``nemoguardrails`` version's class hierarchy.
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Any

from fabric_nemo_sidecar.literal_filter import LiteralJailbreakFilter
from fabric_nemo_sidecar.rails import CheckAction, EngineResult

if TYPE_CHECKING:
    from nemoguardrails import LLMRails  # type: ignore[import-not-found]

_DEFAULT_RAIL = "unknown"

# Rail types whose stop signal should translate to action="block".
# Dialog rails ("input", "dialog", "output") are the user-facing
# enforcement layers; "generation" rails (the LLM call) are not
# enforcement and a stop there means a generation error, which we
# surface separately rather than treating as a guardrail decision.
_BLOCKING_RAIL_TYPES = frozenset({"input", "dialog", "output"})


def _get(obj: Any, key: str) -> Any:
    """Look up ``key`` on a dict or as an attribute on a pydantic model.

    Returns ``None`` if the key is absent rather than raising — the
    response shape varies across ``nemoguardrails`` versions and we
    treat missing keys as "feature not reported."
    """

    if obj is None:
        return None
    if isinstance(obj, dict):
        return obj.get(key)
    return getattr(obj, key, None)


def _coerce_action(raw: object) -> CheckAction:
    """Map a NeMo rails verdict string to our fixed action vocabulary.

    Used for the **legacy** ``rails_info["action"]`` path; the modern
    path derives the action from ``activated_rails`` and never goes
    through this coercion. Fail-closed to ``block`` if the value is
    not one of the four allowed strings.
    """

    if isinstance(raw, str) and raw in ("allow", "redact", "block", "warn"):
        return raw  # type: ignore[return-value]
    return "block"


def _find_stopping_rail(activated_rails: Any) -> Any | None:
    """Return the first activated rail that stopped execution, or None.

    Accepts a list of dicts or a list of pydantic ``ActivatedRail``
    instances; iterates with :func:`_get` so both shapes work.
    """

    if not activated_rails:
        return None
    try:
        iterator = iter(activated_rails)
    except TypeError:
        return None
    for rail in iterator:
        rail_type = _get(rail, "type") or ""
        if rail_type not in _BLOCKING_RAIL_TYPES:
            continue
        if _get(rail, "stop") is True:
            return rail
        decisions = _get(rail, "decisions") or ()
        try:
            if "stop" in decisions:
                return rail
        except TypeError:
            continue
    return None


class NemoRailsEngine:
    """Wrap an ``LLMRails`` instance as a :class:`RailsEngine`.

    ``LLMRails.generate`` is synchronous and returns a string or a
    ``GenerationResponse`` for a single-turn completion. That text is
    an **assistant completion**, never a rewritten copy of the value we
    submitted — ``generate()`` gives us no way to tell the two apart.
    It is therefore used only to derive ``block_response`` (the canned
    refusal a stopping rail emitted); ``modified_value`` echoes the
    request value verbatim.

    The single exception is the legacy ``rails_info`` path, where the
    engine explicitly declares ``action="redact"``: a redact verdict is
    the one action whose contract is "here is a transformation of the
    value you handed me", so its payload is honoured.

    Returning the completion as ``modified_value`` on an ``allow`` was
    defect #9: the SDK chain trusts ``modified_value`` and silently
    replaced the user's prompt with a generated reply, with no policy
    recorded. See ``sdk/python/src/fabric/_chain.py``.

    The action + rail are derived from ``response.log.activated_rails``
    when present, falling back to a legacy ``rails_info`` dict for
    older stubs.

    An optional :class:`LiteralJailbreakFilter` runs before
    ``LLMRails.generate()``. When the filter matches, the engine
    returns an ``action="block"`` result immediately without invoking
    NeMo at all — this gives a deterministic, embedding-free
    jailbreak-prefilter that does not depend on NeMo's canonical-form
    matching (which over-fires under FastEmbed with the starter
    pattern set). The filter is opt-in; ``literal_filter=None``
    preserves the prior behavior of forwarding every request to NeMo.
    """

    __slots__ = ("_literal_filter", "_rails")

    def __init__(
        self,
        rails: LLMRails,
        *,
        literal_filter: LiteralJailbreakFilter | None = None,
    ) -> None:
        self._rails = rails
        self._literal_filter = literal_filter

    @property
    def literal_filter(self) -> LiteralJailbreakFilter | None:
        return self._literal_filter

    def check(self, phase: str, path: str, value: str) -> EngineResult:
        # Pre-filter: deterministic literal substring check. If it
        # fires, we never touch NeMo — the synthetic block result
        # carries the filter's rail name so downstream consumers can
        # distinguish literal hits from Colang flow hits.
        if self._literal_filter is not None and phase == "input":
            match = self._literal_filter.check(value)
            if match is not None:
                return EngineResult(
                    allowed=False,
                    action="block",
                    rail=match.rail,
                    block_response=match.block_response,
                    modified_value=value,
                )

        messages: list[dict[str, Any]] = [{"role": "user", "content": value}]
        try:
            response: Any = self._rails.generate(
                messages=messages,
                options={"log": {"activated_rails": True}},
            )
        except TypeError:
            # Older ``LLMRails.generate`` signatures (and test fakes
            # that do not accept kwargs) — retry without ``options``.
            response = self._rails.generate(messages=messages)
        return _parse_response(value, response)


def _extract_generated_content(response: Any, input_value: str) -> str:
    """Return the text ``generate()`` produced, or the original value.

    This is the **assistant completion**, not a rewrite of the input.
    It feeds :func:`_interpret_activated_rails`, which turns it into a
    ``block_response`` when a rail stopped execution. It must not be
    handed back as ``modified_value`` (defect #9).

    Looks at ``response["content"]`` first, then falls back to
    ``response["response"][-1]["content"]`` (the modern
    ``GenerationResponse`` shape). An empty string in either field is
    treated as "nothing generated" and falls back to ``input_value``,
    which also makes the ``generated != input_value`` test in
    :func:`_interpret_activated_rails` correctly report "no canned
    refusal".
    """

    content = _get(response, "content")
    if isinstance(content, str) and content:
        return content
    outer_response = _get(response, "response")
    if isinstance(outer_response, list) and outer_response:
        last = outer_response[-1]
        last_content = _get(last, "content")
        if isinstance(last_content, str) and last_content:
            return last_content
    return input_value


def _interpret_activated_rails(
    activated_rails: Any,
    generated: str,
    input_value: str,
) -> tuple[str, CheckAction, str | None]:
    """Translate a non-empty ``activated_rails`` list into our verdict
    triple ``(rail_name, action, block_response)``.

    A rail with ``stop == True`` (or ``"stop"`` in ``decisions``) and
    a blocking type translates to ``action="block"``. ``generated`` is
    the assistant completion; when a rail stopped and the completion
    differs from the submitted value it is the rail's canned refusal,
    so it travels in ``block_response`` — never in ``modified_value``.

    Otherwise the first rail's name is recorded as a non-blocking
    policy hit so the chain layer can surface it in ``policies_fired``
    while keeping ``action="allow"``.
    """

    stopping_rail = _find_stopping_rail(activated_rails)
    if stopping_rail is not None:
        rail_name = str(_get(stopping_rail, "name") or _DEFAULT_RAIL)
        block_response = generated if generated and generated != input_value else None
        return rail_name, "block", block_response

    first_rail = next(iter(activated_rails), None)
    rail_name = _DEFAULT_RAIL
    if first_rail is not None:
        first_name = _get(first_rail, "name")
        if first_name:
            rail_name = str(first_name)
    return rail_name, "allow", None


def _interpret_legacy_rails_info(response: Any) -> tuple[str, CheckAction, str | None]:
    """Translate a pre-0.10 ``rails_info`` dict into our verdict triple.

    Returns ``(_DEFAULT_RAIL, "allow", None)`` if no ``rails_info`` is
    present, so a response that uses neither the modern nor the legacy
    shape falls through cleanly as an allow.
    """

    rails_info = _get(response, "rails_info") or {}
    if not rails_info:
        return _DEFAULT_RAIL, "allow", None

    rail_name = str(_get(rails_info, "rail") or _DEFAULT_RAIL)
    legacy_action = _get(rails_info, "action")
    action: CheckAction = "allow" if legacy_action is None else _coerce_action(legacy_action)
    block_value = _get(rails_info, "block_response")
    block_response = block_value if isinstance(block_value, str) else None
    return rail_name, action, block_response


def _parse_response(input_value: str, response: Any) -> EngineResult:
    if response is None:
        # No response — fail-closed; the sidecar caller treats action
        # != "allow" as policy-fired, so block_response stays None and
        # the chain layer will surface the empty modified_value.
        return EngineResult(
            allowed=False,
            action="block",
            rail=_DEFAULT_RAIL,
            block_response=None,
            modified_value=input_value,
        )

    if isinstance(response, str):
        # A bare string from ``generate()`` IS the assistant completion.
        # Echo the submitted value instead (defect #9).
        return EngineResult(
            allowed=True,
            action="allow",
            rail=_DEFAULT_RAIL,
            block_response=None,
            modified_value=input_value,
        )

    generated = _extract_generated_content(response, input_value)

    # Modern path: response.log.activated_rails. Legacy fall-through:
    # response.rails_info.
    log = _get(response, "log")
    activated_rails = _get(log, "activated_rails") if log is not None else None
    if activated_rails:
        rail, action, block_response = _interpret_activated_rails(
            activated_rails, generated, input_value
        )
    else:
        rail, action, block_response = _interpret_legacy_rails_info(response)

    allowed = action in ("allow", "redact", "warn")
    # ``redact`` is the only action whose payload is a declared
    # transformation of the submitted value; every other action gets
    # the value back byte-for-byte so a generated reply can never
    # masquerade as a rewritten input. Only the legacy ``rails_info``
    # path can produce ``redact``, so the modern Colang path always
    # echoes — which is correct: ``generate()`` gives us no way to
    # distinguish a rewritten input from a reply.
    modified_value = generated if action == "redact" else input_value
    return EngineResult(
        allowed=allowed,
        action=action,
        rail=rail,
        block_response=block_response,
        modified_value=modified_value,
    )


def build_default_engine(
    config_path: str,
    *,
    literal_filter: LiteralJailbreakFilter | None = None,
) -> NemoRailsEngine:
    """Construct a :class:`NemoRailsEngine` from a Colang config dir.

    Raises :class:`ImportError` if ``nemoguardrails`` is not installed.

    ``literal_filter`` is forwarded to the engine. When supplied, the
    engine runs the filter against every input-phase value before
    invoking ``LLMRails.generate()``.
    """

    from nemoguardrails import LLMRails, RailsConfig  # noqa: PLC0415

    config = RailsConfig.from_path(config_path)
    return NemoRailsEngine(LLMRails(config), literal_filter=literal_filter)
