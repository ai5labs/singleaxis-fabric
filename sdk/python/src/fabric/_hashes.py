# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Validation for caller-supplied digest metadata.

Hash-labelled telemetry fields are a privacy boundary. Accepting arbitrary
strings under a ``*_hash`` key would let raw content bypass the Collector's
exact-key allowlist, so recorder APIs require lowercase SHA-256 hex.
"""

from __future__ import annotations

import re
from collections.abc import Iterable

_SHA256_HEX = re.compile(r"^[0-9a-f]{64}$")


def require_sha256_hex(field_name: str, value: str) -> str:
    """Return ``value`` when it is lowercase SHA-256 hex; otherwise fail."""
    if not isinstance(value, str):
        raise TypeError(f"{field_name} must be str, got {type(value).__name__}")
    if _SHA256_HEX.fullmatch(value) is None:
        raise ValueError(f"{field_name} must be exactly 64 lowercase SHA-256 hex characters")
    return value


def require_sha256_hex_values(field_name: str, values: Iterable[str]) -> tuple[str, ...]:
    """Validate and freeze a sequence of SHA-256 hex values."""
    return tuple(require_sha256_hex(field_name, value) for value in values)
