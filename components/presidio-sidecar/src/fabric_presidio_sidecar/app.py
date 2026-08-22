# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""FastAPI app factory for the Presidio sidecar."""

from __future__ import annotations

import hmac
import os

from fastapi import Depends, FastAPI, Header, HTTPException, Request
from fastapi.params import Depends as DependsParam

from fabric_presidio_sidecar._version import __version__
from fabric_presidio_sidecar.redactor import (
    PassthroughAnalyzer,
    PIIAnalyzer,
    RedactionMode,
    RedactionRequest,
    RedactionResponse,
    Redactor,
)

_ASGI_SERVER_TUPLE_LENGTH = 2


def _auth_token() -> str:
    """Shared token for the TCP ``/v1/*`` routes, read from the env.

    Set via ``FABRIC_SIDECAR_TOKEN`` (chart value ``auth.tokenSecret`` wires
    it from a Secret). Unset or empty → auth is disabled and behaviour
    matches pre-0.7 releases exactly. UDS callers rely on socket
    filesystem permissions and bypass this TCP-only check.
    """

    return os.getenv("FABRIC_SIDECAR_TOKEN", "")


def _is_uds_request(request: Request) -> bool:
    """Return true when uvicorn accepted the request on a Unix socket.

    Uvicorn exposes a UDS listener as ``(path, None)`` in the ASGI
    ``server`` scope. UDS access is already controlled by filesystem
    permissions, so the shared token protects only the TCP surface.
    """

    server = request.scope.get("server")
    return (
        isinstance(server, tuple)
        and len(server) == _ASGI_SERVER_TUPLE_LENGTH
        and server[1] is None
    )


def build_app(
    analyzer: PIIAnalyzer | None = None,
    tenant_key: bytes | None = None,
    mode: RedactionMode = "hmac",
) -> FastAPI:
    """Construct the FastAPI app with the given analyzer.

    ``tenant_key`` is required for production deployments; the CLI
    enforces this via the ``--tenant-key-file`` flag. Tests that do not
    care about hashing behaviour may pass an arbitrary non-empty byte
    string (the ``PassthroughAnalyzer`` never invokes the HMAC path).
    Passing ``None`` or the default sentinel is rejected so no caller
    can accidentally ship deterministic, cross-deployment HMACs.

    ``mode`` selects the redaction strategy. ``hmac`` (default)
    preserves backward compatibility by returning a tenant-scoped
    HMAC-SHA256 of the whole value. ``tag`` replaces each detected
    PII span in-place with a category-typed placeholder like
    ``<EMAIL_1>``, which is the recommended setting when the
    redacted text is fed back to an LLM.

    When the ``FABRIC_SIDECAR_TOKEN`` env var is set, TCP requests to
    every ``/v1/*`` route additionally require an ``X-Fabric-Token``
    header matching it (constant-time compare). See :func:`_auth_token`.
    """

    if tenant_key is None or not tenant_key or tenant_key == b"change-me":
        raise ValueError(
            "tenant_key must be a real, non-sentinel byte string; refusing "
            "to build an app with a default key so HMACs are not reversible "
            "across deployments"
        )
    redactor = Redactor(analyzer or PassthroughAnalyzer(), tenant_key, mode=mode)
    app = FastAPI(
        title="fabric-presidio-sidecar", version=__version__, docs_url=None, redoc_url=None
    )

    token = _auth_token()

    async def require_token(
        request: Request,
        x_fabric_token: str = Header(default="", alias="X-Fabric-Token"),
    ) -> None:
        if _is_uds_request(request):
            return
        if not hmac.compare_digest(x_fabric_token.encode(), token.encode()):
            raise HTTPException(
                status_code=401,
                detail="missing or invalid X-Fabric-Token header",
            )

    # Applied to /v1/* only; /healthz stays open for k8s probes. Empty
    # list when no token is configured → zero behavioural change.
    auth_deps: list[DependsParam] = [Depends(require_token)] if token else []

    @app.get("/healthz")
    def healthz() -> dict[str, str]:
        return {"status": "ok", "version": __version__}

    @app.post("/v1/redact", response_model=RedactionResponse, dependencies=auth_deps)
    def redact(request: RedactionRequest) -> RedactionResponse:
        return redactor.redact(request)

    return app
