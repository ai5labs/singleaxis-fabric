# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Sidecar entry point.

Serves on a Unix domain socket by default, HTTP on TCP for local dev.

Concurrency and timeout knobs (env vars, all optional):

- ``FABRIC_LIMIT_CONCURRENCY`` (default 16) — uvicorn ``limit_concurrency``.
  Caps the number of in-flight requests so ``/check`` cannot starve
  ``/healthz`` by saturating the ~40-thread default pool.
- ``FABRIC_REQUEST_TIMEOUT_MS`` (default 800) — per-request internal
  timeout around ``LLMRails.generate``. Read by ``app.build_app``.
"""

from __future__ import annotations

import argparse
import logging
import os
import socket
import sys
from typing import TYPE_CHECKING

import uvicorn

from fabric_nemo_sidecar.app import build_app

if TYPE_CHECKING:
    from fabric_nemo_sidecar.literal_filter import LiteralJailbreakFilter
    from fabric_nemo_sidecar.rails import RailsEngine

# Re-exported so tests (and any tooling) can monkeypatch the symbol the
# CLI actually calls; the explicit __all__ keeps mypy's no-implicit-reexport
# happy when build_app is accessed via this module.
__all__ = ["bind_uds", "build_app", "main"]

logger = logging.getLogger("fabric_nemo_sidecar")

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
        # S103: 0o666 is deliberate and matches uvicorn's own UDS mode.
        # The socket is reachable only through its directory, which is
        # what actually gates access; an unreadable socket would simply
        # make the sidecar unusable by its co-located caller.
        os.chmod(path, 0o666)  # noqa: S103
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


def _build_literal_filter(
    parser: argparse.ArgumentParser,
    args: argparse.Namespace,
) -> LiteralJailbreakFilter | None:
    """Construct an optional :class:`LiteralJailbreakFilter` from CLI
    args. ``None`` if neither filter flag was supplied. Errors out via
    ``parser.error`` (which raises ``SystemExit``) if the two filter
    flags are passed together.
    """

    if args.literal_jailbreak_patterns and args.enable_default_literal_filter:
        parser.error(
            "--literal-jailbreak-patterns and "
            "--enable-default-literal-filter are mutually exclusive"
        )

    if not (args.literal_jailbreak_patterns or args.enable_default_literal_filter):
        return None

    from fabric_nemo_sidecar.literal_filter import (  # noqa: PLC0415
        LiteralJailbreakFilter,
        load_patterns_file,
    )

    if args.literal_jailbreak_patterns:
        patterns = load_patterns_file(args.literal_jailbreak_patterns)
        return LiteralJailbreakFilter(patterns=patterns)
    return LiteralJailbreakFilter()


def _build_engine(
    args: argparse.Namespace,
    literal_filter: LiteralJailbreakFilter | None,
) -> RailsEngine | None:
    """Construct the rails engine from CLI args. Returns ``None`` when
    ``--allow-passthrough`` is set and no ``--rails-config`` is given;
    the caller logs a warning so the misconfiguration is visible in
    pod logs.
    """

    if args.rails_config:
        from fabric_nemo_sidecar.nemo_adapter import build_default_engine  # noqa: PLC0415

        return build_default_engine(args.rails_config, literal_filter=literal_filter)

    # --allow-passthrough was set; emit a startup-time warning so the
    # operator can see this in pod logs. The rail name on every /check
    # response stamps PASSTHROUGH_FAIL_OPEN so any downstream
    # dashboard surfaces the misconfiguration.
    logger.warning(
        "NeMo sidecar starting in PASSTHROUGH mode "
        "(--allow-passthrough): jailbreak/policy defence is "
        "disabled. DO NOT use in production."
    )
    return None


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="fabric-nemo-sidecar")
    parser.add_argument("--uds", help="Unix domain socket path. Mutually exclusive with --port.")
    parser.add_argument("--port", type=int, help="TCP port (local dev only)")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument(
        "--rails-config",
        help="Directory containing the Colang rails config. Required "
        "for production. Pass --allow-passthrough to opt into the "
        "fail-open passthrough engine for local smoke tests only.",
    )
    parser.add_argument(
        "--allow-passthrough",
        action="store_true",
        help="Permit running without --rails-config. The sidecar will "
        "use the passthrough engine which allows everything — only for "
        "local development. Refused without this flag in 0.1.3+.",
    )
    parser.add_argument(
        "--literal-jailbreak-patterns",
        metavar="PATH",
        help="Optional path to a one-pattern-per-line file. When set, "
        "the sidecar runs a deterministic case-insensitive substring "
        "filter against every input value before invoking NeMo. A "
        "match returns action='block' immediately without an "
        "LLMRails.generate() call. Use to harden the starter rails "
        "against NeMo's embedding-based canonical-form matching, "
        "which is too loose under FastEmbed for short-phrase patterns.",
    )
    parser.add_argument(
        "--enable-default-literal-filter",
        action="store_true",
        help="Enable the built-in literal jailbreak-pattern filter "
        "(see fabric_nemo_sidecar.literal_filter.DEFAULT_JAILBREAK_PATTERNS). "
        "Mutually exclusive with --literal-jailbreak-patterns.",
    )
    args = parser.parse_args(argv)

    if args.uds and args.port:
        parser.error("--uds and --port are mutually exclusive")
    if not args.uds and not args.port:
        parser.error("one of --uds or --port is required")

    if not args.rails_config and not args.allow_passthrough:
        parser.error(
            "--rails-config is required. Without it the sidecar would "
            "fall back to a passthrough engine that allows everything, "
            "silently disabling jailbreak/policy defence. Pass "
            "--allow-passthrough explicitly for local smoke tests."
        )

    literal_filter = _build_literal_filter(parser, args)
    engine = _build_engine(args, literal_filter)

    # Concurrency env-var: parse robustly. A non-int value should
    # surface a clear error rather than crashing the whole sidecar
    # start with "ValueError: invalid literal for int()".
    raw_conc = os.getenv("FABRIC_LIMIT_CONCURRENCY", "16")
    try:
        limit_concurrency = int(raw_conc)
    except ValueError:
        parser.error(f"FABRIC_LIMIT_CONCURRENCY={raw_conc!r} is not a valid integer")
        return 2  # pragma: no cover (parser.error raises)

    app = build_app(engine=engine)

    kwargs: dict[str, object] = {
        "app": app,
        "log_config": None,
        "limit_concurrency": limit_concurrency,
        "timeout_keep_alive": 5,
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
