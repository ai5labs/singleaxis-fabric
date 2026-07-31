# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Sidecar entry point.

Serves on a Unix domain socket by default, HTTP on TCP for local dev.
"""

from __future__ import annotations

import argparse
import logging
import os
import socket
import sys

import uvicorn

from fabric_presidio_sidecar.app import build_app

# Re-exported so tests (and any tooling) can monkeypatch the symbol the
# CLI actually calls; the explicit __all__ keeps mypy's no-implicit-reexport
# happy when build_app is accessed via this module.
__all__ = ["bind_uds", "build_app", "main"]

logger = logging.getLogger("fabric_presidio_sidecar")

# Backlog for the listening socket. Matches uvicorn's own default so
# behaviour is unchanged from letting uvicorn bind the socket itself.
_UDS_BACKLOG = 2048


def bind_uds(path: str) -> socket.socket:
    """Bind and return a listening Unix domain socket at ``path``.

    We bind the socket here rather than handing ``uds=`` to uvicorn so
    that *we* own the permission step. uvicorn's own UDS path calls
    ``os.chmod`` unconditionally and dies if the filesystem refuses it;
    Docker Desktop's macOS bind mounts (virtiofs) raise ``EINVAL`` on
    ``chmod`` of a socket, which made the sidecar impossible to run
    against a bind-mounted socket directory. Permissions are still
    applied wherever the filesystem supports them — we only degrade to a
    warning when they cannot be, instead of refusing to start.
    """

    if os.path.exists(path):
        os.unlink(path)
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.bind(path)
    try:
        # Owner/group access supports co-located callers sharing the pod
        # fsGroup without exposing the redaction socket to every local user.
        os.chmod(path, 0o660)
    except OSError as exc:
        logger.warning(
            "could not set permissions on %s (%s); continuing with the "
            "filesystem default. Some bind-mounted filesystems (notably "
            "Docker Desktop on macOS) do not support chmod on sockets. "
            "Callers must already have access via directory permissions.",
            path,
            exc,
        )
    sock.listen(_UDS_BACKLOG)
    return sock


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="fabric-presidio-sidecar")
    parser.add_argument("--uds", help="Unix domain socket path. Mutually exclusive with --port.")
    parser.add_argument("--port", type=int, help="TCP port (local dev only)")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument(
        "--tenant-key-file",
        required=True,
        help=(
            "Path to the file containing the tenant HMAC key (bytes). "
            "Required: the sidecar refuses to start without a real tenant "
            "key so that HMACs are not reversible across deployments."
        ),
    )
    parser.add_argument(
        "--allow-passthrough",
        action="store_true",
        help=(
            "Allow the sidecar to start with the PassthroughAnalyzer "
            "(redacts nothing) when the [presidio] extra is not "
            "installed. Without this flag the sidecar fails fast so a "
            "misconfigured production deployment cannot silently ship "
            "a no-op redactor."
        ),
    )
    parser.add_argument(
        "--redaction-mode",
        choices=["hmac", "tag"],
        default="hmac",
        help=(
            "Redaction strategy when PII is detected. 'hmac' (default) "
            "returns a tenant-scoped HMAC-SHA256 of the full value. 'tag' "
            "replaces each detected entity in-place with a category-typed "
            "placeholder like <EMAIL_1>. Default stays 'hmac' for backward "
            "compatibility; tag mode is recommended for any agent that "
            "feeds the redacted value back to an LLM (multi-turn)."
        ),
    )
    args = parser.parse_args(argv)

    if args.uds and args.port:
        parser.error("--uds and --port are mutually exclusive")
    if not args.uds and not args.port:
        parser.error("one of --uds or --port is required")

    with open(args.tenant_key_file, "rb") as fh:
        tenant_key = fh.read().strip()
    if not tenant_key or tenant_key == b"change-me":
        parser.error(
            "tenant key file is empty or contains the default sentinel "
            "'change-me'; supply a real key via --tenant-key-file"
        )

    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(name)s %(levelname)s %(message)s",
    )
    analyzer = None
    try:
        from fabric_presidio_sidecar.presidio_analyzer import (  # noqa: PLC0415
            build_default_analyzer,
        )

        analyzer = build_default_analyzer()
        logger.info("wired real PresidioAnalyzer (presidio-analyzer + spaCy)")
    except ImportError as exc:
        if not args.allow_passthrough:
            parser.error(
                f"presidio extras not installed ({exc}); refusing to start "
                "with PassthroughAnalyzer (would silently redact nothing). "
                "Install the [presidio] extra, or pass --allow-passthrough "
                "for explicit no-op mode (dev / smoke only)."
            )
        logger.warning(
            "starting with PassthroughAnalyzer (no PII redaction); --allow-passthrough set"
        )

    app = build_app(analyzer=analyzer, tenant_key=tenant_key, mode=args.redaction_mode)
    logger.info("redaction mode: %s", args.redaction_mode)

    kwargs: dict[str, object] = {
        "app": app,
        "log_config": None,
    }
    # Held for the lifetime of the server: uvicorn dups the descriptor,
    # so the socket object must not be garbage collected before it runs.
    uds_sock: socket.socket | None = None
    if args.uds:
        uds_sock = bind_uds(args.uds)
        kwargs["fd"] = uds_sock.fileno()
    else:
        kwargs["host"] = args.host
        kwargs["port"] = args.port

    try:
        uvicorn.run(**kwargs)  # type: ignore[arg-type]
    finally:
        if uds_sock is not None:
            uds_sock.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
