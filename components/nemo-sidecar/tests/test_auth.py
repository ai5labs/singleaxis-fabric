# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0

"""Shared-token auth on the TCP /v1/* routes (FABRIC_SIDECAR_TOKEN).

Auth must be HTTP-header-only so UDS callers can send the same header;
it must be fully off when the env var is unset.
"""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from fabric_nemo_sidecar import build_app

_CHECK = {"phase": "input", "path": "input", "value": "hello"}


def test_v1_open_when_token_unset(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("FABRIC_SIDECAR_TOKEN", raising=False)
    app = build_app()
    with TestClient(app) as c:
        assert c.get("/healthz").status_code == 200
        resp = c.post("/v1/check", json=_CHECK)
        assert resp.status_code == 200
        assert resp.json()["allowed"] is True


def test_v1_rejects_missing_and_wrong_token(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("FABRIC_SIDECAR_TOKEN", "s3cret")
    app = build_app()
    with TestClient(app) as c:
        # No header at all.
        assert c.post("/v1/check", json=_CHECK).status_code == 401
        # Wrong value — including a prefix of the real token.
        assert (
            c.post("/v1/check", json=_CHECK, headers={"X-Fabric-Token": "nope"}).status_code == 401
        )
        assert (
            c.post("/v1/check", json=_CHECK, headers={"X-Fabric-Token": "s3c"}).status_code == 401
        )


def test_v1_accepts_matching_token(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("FABRIC_SIDECAR_TOKEN", "s3cret")
    app = build_app()
    with TestClient(app) as c:
        resp = c.post("/v1/check", json=_CHECK, headers={"X-Fabric-Token": "s3cret"})
        assert resp.status_code == 200
        assert resp.json()["allowed"] is True


def test_healthz_stays_open_with_token_set(monkeypatch: pytest.MonkeyPatch) -> None:
    # Probes cannot carry secrets; /healthz must stay unauthenticated.
    monkeypatch.setenv("FABRIC_SIDECAR_TOKEN", "s3cret")
    app = build_app()
    with TestClient(app) as c:
        assert c.get("/healthz").status_code == 200


def test_empty_token_value_disables_auth(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("FABRIC_SIDECAR_TOKEN", "")
    app = build_app()
    with TestClient(app) as c:
        assert c.post("/v1/check", json=_CHECK).status_code == 200
