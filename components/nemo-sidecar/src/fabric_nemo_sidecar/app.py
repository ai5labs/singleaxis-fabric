# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""FastAPI app factory for the NeMo sidecar.

``/v1/check`` runs the rails engine under a small dedicated
``ThreadPoolExecutor`` with an internal per-request timeout. This
protects ``/healthz`` — a single slow ``LLMRails.generate()`` used to
pin the shared default threadpool and block liveness probes; now each
/check slot has a bounded wallclock budget.

Timeout is read from ``FABRIC_REQUEST_TIMEOUT_MS`` at app-build time
(default 800). Concurrency of the dedicated pool is
``FABRIC_LIMIT_CONCURRENCY`` (default 16) so it doesn't outstrip the
uvicorn-level cap that ``__main__`` also applies.

When ``FABRIC_SIDECAR_TOKEN`` is set, TCP requests to ``/v1/check``
additionally require an ``X-Fabric-Token`` HTTP header matching it
(constant-time compare). UDS requests rely on socket filesystem
permissions and bypass this TCP-only check.
"""

from __future__ import annotations

import hmac
import os
from collections.abc import AsyncIterator
from concurrent.futures import ThreadPoolExecutor
from concurrent.futures import TimeoutError as FutureTimeoutError
from contextlib import asynccontextmanager

from fastapi import Depends, FastAPI, Header, HTTPException, Request
from fastapi.params import Depends as DependsParam

from fabric_nemo_sidecar._version import __version__
from fabric_nemo_sidecar.rails import (
    CheckRequest,
    CheckResponse,
    PassthroughEngine,
    RailsChecker,
    RailsEngine,
)

_ASGI_SERVER_TUPLE_LENGTH = 2


def _timeout_seconds() -> float:
    """Read the per-request timeout from the env. Must be > 0."""

    raw = os.getenv("FABRIC_REQUEST_TIMEOUT_MS", "800")
    try:
        ms = int(raw)
    except ValueError:
        ms = 800
    if ms <= 0:
        ms = 800
    return ms / 1000.0


def _pool_size() -> int:
    raw = os.getenv("FABRIC_LIMIT_CONCURRENCY", "16")
    try:
        n = int(raw)
    except ValueError:
        n = 16
    return max(1, n)


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


def build_app(engine: RailsEngine | None = None) -> FastAPI:
    """Construct the FastAPI app with the given rails engine."""

    checker = RailsChecker(engine or PassthroughEngine())

    timeout_s = _timeout_seconds()
    executor = ThreadPoolExecutor(
        max_workers=_pool_size(),
        thread_name_prefix="fabric-nemo-check",
    )

    @asynccontextmanager
    async def lifespan(_: FastAPI) -> AsyncIterator[None]:
        try:
            yield
        finally:
            executor.shutdown(wait=False, cancel_futures=True)

    app = FastAPI(
        title="fabric-nemo-sidecar",
        version=__version__,
        docs_url=None,
        redoc_url=None,
        lifespan=lifespan,
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

    @app.post("/v1/check", response_model=CheckResponse, dependencies=auth_deps)
    def check(request: CheckRequest) -> CheckResponse:
        future = executor.submit(checker.check, request)
        try:
            return future.result(timeout=timeout_s)
        except FutureTimeoutError as exc:
            # Fail-closed: a timeout is a policy failure, not a pass.
            # The SDK treats 504 as "rails unavailable → block".
            future.cancel()
            raise HTTPException(
                status_code=504,
                detail=(
                    f"rails check exceeded {int(timeout_s * 1000)}ms internal "
                    "timeout (FABRIC_REQUEST_TIMEOUT_MS)"
                ),
            ) from exc

    return app
