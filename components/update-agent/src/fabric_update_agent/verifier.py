# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""The verifier ties the three checks together.

Public contract:

    result = Verifier(config).verify(manifest)
    # result.allowed: bool
    # result.reason:  human-readable deny string (None on allow)
    # result.signer_id: who signed it (None on deny)

Checks, in order:

1. Signature annotation present → cryptographic verify against one
   of the trusted keys.
2. Version-constraint annotation → must include the installed
   Fabric version.
3. Schema registry → ``(apiVersion, kind)``-specific JSON Schema.

Manifests missing *both* the signature and version annotations are
allowed when ``fail_closed=False`` (off-path resources, like user
CRDs that happen to live in the same namespace). With
``fail_closed=True`` (default), every manifest must carry the two
annotations."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

import yaml

from .config import VerifierConfig
from .schema import SchemaError, SchemaRegistry
from .signatures import (
    SIGNATURE_ANNOTATION,
    SignatureError,
)
from .signatures import (
    verify as verify_signature,
)
from .version import VERSION_CONSTRAINT_ANNOTATION, VersionError
from .version import check as check_version


class VerifierError(Exception):
    """Unrecoverable configuration error — distinct from deny."""


# --- Profile-lock admission backstop ---
#
# The render-time invariant (charts/fabric/templates/_helpers.tpl,
# fabric.validateProfileLocks) pins the eu-ai-act-high-risk locked
# controls at install/upgrade time. It cannot see direct
# ``kubectl edit``/``apply`` drift afterwards. When the chart enables
# ``enforce_locked_fields``, this verifier additionally denies any
# ConfigMap carrying the otel-collector config naming whose rendered
# collector config drops a locked control — regardless of annotations.
#
# Scope note: the ValidatingWebhookConfiguration's namespaceSelector
# already restricts admission to webhook.watchedNamespaces, so "watched
# namespaces" is enforced by the VWC, not re-checked here.

_OTEL_CONFIG_NAME_SUFFIX = "-otel-collector-config"
_OTEL_CONFIG_DATA_KEY = "config.yaml"
_FABRIC_PART_OF_LABEL = "app.kubernetes.io/part-of"
_OTEL_NAME_LABEL = "app.kubernetes.io/name"


def _is_otel_collector_config(manifest: dict[str, Any]) -> bool:
    """Match how charts/fabric/charts/otel-collector names its config:
    ``<release>-otel-collector-config`` (or the labelled equivalent
    when fullnameOverride is in play)."""
    if manifest.get("kind") != "ConfigMap":
        return False
    meta = manifest.get("metadata")
    if not isinstance(meta, dict):
        return False
    name = meta.get("name")
    if isinstance(name, str) and name.endswith(_OTEL_CONFIG_NAME_SUFFIX):
        return True
    labels = meta.get("labels")
    return isinstance(labels, dict) and (
        labels.get(_FABRIC_PART_OF_LABEL) == "fabric"
        and labels.get(_OTEL_NAME_LABEL) == "otel-collector"
    )


def _locked_field_violation(manifest: dict[str, Any]) -> str | None:
    """Deny reason when the collector config drops a locked control,
    else None. Fails closed on unparseable/malformed payloads."""
    data = manifest.get("data")
    raw = data.get(_OTEL_CONFIG_DATA_KEY) if isinstance(data, dict) else None
    if not isinstance(raw, str):
        return (
            f"otel-collector ConfigMap has no {_OTEL_CONFIG_DATA_KEY!r} data; "
            "profile-locked guard/redact state cannot be confirmed"
        )
    try:
        parsed = yaml.safe_load(raw)
    except yaml.YAMLError:
        return (
            f"otel-collector ConfigMap {_OTEL_CONFIG_DATA_KEY!r} does not parse; "
            "denied under profile lock (fail closed)"
        )
    if not isinstance(parsed, dict):
        return f"otel-collector ConfigMap {_OTEL_CONFIG_DATA_KEY!r} is not a mapping"
    processors = parsed.get("processors")
    if not isinstance(processors, dict):
        return (
            "otel-collector config has no processors mapping; "
            "profile-locked state cannot be confirmed"
        )
    guard = processors.get("fabricguard")
    if not isinstance(guard, dict):
        return (
            "profile-locked control otel-collector.fabric.guard.enabled is off: "
            "the fabricguard processor is missing from the collector config"
        )
    if guard.get("drop_unknown_classes") is not True:
        return (
            "profile-locked control otel-collector.fabric.guard.dropUnknownClasses "
            "is not true in the collector config"
        )
    if not isinstance(processors.get("fabricredact"), dict):
        return (
            "profile-locked control otel-collector.fabric.redact.enabled is off: "
            "the fabricredact processor is missing from the collector config"
        )
    return None


@dataclass(frozen=True)
class VerificationResult:
    allowed: bool
    reason: str | None = None
    signer_id: str | None = None


class Verifier:
    def __init__(
        self,
        config: VerifierConfig,
        schema_registry: SchemaRegistry | None = None,
    ) -> None:
        self._config = config
        self._schemas = schema_registry or SchemaRegistry()

    def verify(self, manifest: dict[str, Any]) -> VerificationResult:
        # Profile-lock backstop runs FIRST: it must also catch
        # unannotated direct edits (the fail_closed branch below would
        # deny those anyway when True, but not with a reason naming the
        # locked control, and not at all when fail_closed=False).
        if self._config.enforce_locked_fields and _is_otel_collector_config(manifest):
            violation = _locked_field_violation(manifest)
            if violation:
                return VerificationResult(allowed=False, reason=violation)

        anns = _annotations(manifest)
        has_signature = SIGNATURE_ANNOTATION in anns
        has_version = VERSION_CONSTRAINT_ANNOTATION in anns

        # Off-path resource (nothing to verify). With fail_closed the
        # manifest must carry the annotations to get through.
        if not has_signature and not has_version:
            if self._config.fail_closed:
                return VerificationResult(
                    allowed=False,
                    reason=(
                        "manifest has no Fabric signature/version annotations and fail_closed=True"
                    ),
                )
            return VerificationResult(allowed=True)

        # One but not both — always a deny; the channel signs every
        # managed resource with both annotations together.
        if not has_signature:
            return VerificationResult(
                allowed=False,
                reason=f"missing annotation {SIGNATURE_ANNOTATION!r}",
            )
        if not has_version:
            return VerificationResult(
                allowed=False,
                reason=f"missing annotation {VERSION_CONSTRAINT_ANNOTATION!r}",
            )

        try:
            signer_id = verify_signature(
                manifest,
                anns[SIGNATURE_ANNOTATION],
                self._config.trusted_keys,
            )
        except SignatureError as e:
            return VerificationResult(allowed=False, reason=str(e))

        try:
            check_version(manifest, self._config.fabric_version)
        except VersionError as e:
            return VerificationResult(allowed=False, reason=str(e))

        try:
            self._schemas.validate(manifest)
        except SchemaError as e:
            return VerificationResult(allowed=False, reason=str(e))

        return VerificationResult(allowed=True, signer_id=signer_id)


def _annotations(manifest: dict[str, Any]) -> dict[str, str]:
    meta = manifest.get("metadata")
    if not isinstance(meta, dict):
        return {}
    anns = meta.get("annotations")
    if not isinstance(anns, dict):
        return {}
    return {k: v for k, v in anns.items() if isinstance(k, str) and isinstance(v, str)}
