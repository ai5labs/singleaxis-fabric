# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0

from __future__ import annotations

import errno
import logging
import os
import shutil
import socket
import stat
import tempfile
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from fabric_nemo_sidecar import __main__ as cli_module

UVICORN_RUN = "fabric_nemo_sidecar.__main__.uvicorn.run"


@pytest.fixture
def sock_dir() -> Iterator[Path]:
    """A directory short enough to hold an AF_UNIX path.

    ``tmp_path`` nests test names several levels deep, which overruns the
    ~104-byte ``sockaddr_un.sun_path`` limit on macOS and BSD. Socket
    paths need their own shallow directory.
    """

    path = Path(tempfile.mkdtemp(prefix="fns-"))
    try:
        yield path
    finally:
        shutil.rmtree(path, ignore_errors=True)


def test_cli_requires_uds_or_port() -> None:
    with pytest.raises(SystemExit):
        cli_module.main([])


def test_cli_rejects_both_uds_and_port(tmp_path: Path) -> None:
    with pytest.raises(SystemExit):
        cli_module.main(["--uds", str(tmp_path / "s.sock"), "--port", "8080"])


def test_cli_invokes_uvicorn_on_uds(sock_dir: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """The CLI binds the UDS itself and hands uvicorn the descriptor.

    We pass ``fd=`` rather than ``uds=`` so that uvicorn never runs its
    unconditional ``os.chmod`` on the socket — see ``bind_uds``. The
    socket must still be created, bound and listening at the requested
    path, which is what a caller actually depends on, so asserting on a
    real ``S_ISSOCK`` at that path is strictly stronger than the old
    ``captured["uds"] == str(sock)`` string check.
    """

    captured: dict[str, Any] = {}

    def fake_run(**kwargs: Any) -> None:
        captured.update(kwargs)

    monkeypatch.setattr(UVICORN_RUN, fake_run)
    sock = sock_dir / "sidecar.sock"
    # `--allow-passthrough` is required because no `--rails-config` is
    # provided; the security hardening in 3a9245d makes that explicit.
    assert cli_module.main(["--uds", str(sock), "--allow-passthrough"]) == 0
    assert "uds" not in captured, "uds= would re-introduce uvicorn's mandatory chmod"
    assert isinstance(captured["fd"], int)
    assert sock.exists()
    assert stat.S_ISSOCK(sock.stat().st_mode), "path must be a real bound socket"
    assert "app" in captured
    # The nemo sidecar's concurrency/timeout knobs must survive the
    # switch to fd-based binding.
    assert captured["limit_concurrency"] == 16
    assert captured["timeout_keep_alive"] == 5
    assert captured["log_config"] is None


def test_cli_invokes_uvicorn_on_tcp(monkeypatch: pytest.MonkeyPatch) -> None:
    captured: dict[str, Any] = {}
    monkeypatch.setattr(UVICORN_RUN, lambda **kw: captured.update(kw))
    assert cli_module.main(["--port", "8081", "--host", "127.0.0.1", "--allow-passthrough"]) == 0
    assert captured["port"] == 8081
    assert captured["host"] == "127.0.0.1"


def test_cli_unlinks_stale_socket(sock_dir: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """A stale path is removed and replaced by a live bound socket.

    Previously the CLI only unlinked the stale entry and left binding to
    uvicorn, so the assertion was simply that the path was gone. Now the
    CLI binds it itself, so the stronger guarantee holds: the leftover
    regular file is replaced by an actual socket.
    """

    sock = sock_dir / "stale.sock"
    sock.write_bytes(b"")
    assert not stat.S_ISSOCK(sock.stat().st_mode)
    monkeypatch.setattr(UVICORN_RUN, lambda **kw: None)
    cli_module.main(["--uds", str(sock), "--allow-passthrough"])
    assert sock.exists()
    assert stat.S_ISSOCK(sock.stat().st_mode)


def test_bind_uds_is_owner_group_only(sock_dir: Path) -> None:
    target = sock_dir / "permissions.sock"
    sock = cli_module.bind_uds(str(target))
    try:
        assert stat.S_IMODE(target.stat().st_mode) == 0o660
    finally:
        sock.close()


def test_bind_uds_survives_chmod_refusal(
    sock_dir: Path,
    monkeypatch: pytest.MonkeyPatch,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Regression: a filesystem that refuses chmod must not stop startup.

    Docker Desktop's macOS bind mounts raise ``EINVAL`` when chmod-ing a
    socket. uvicorn's own UDS path calls ``os.chmod`` unconditionally, so
    both guardrail sidecars crashed on startup and the documented compose
    harness could not run on macOS at all. ``bind_uds`` must still return
    a listening socket, and must say so in the log rather than silently
    proceeding with unknown permissions.
    """

    real_chmod = os.chmod

    def refusing_chmod(path: Any, mode: int, *a: Any, **kw: Any) -> None:
        if str(path).endswith(".sock"):
            raise OSError(errno.EINVAL, "Invalid argument")
        real_chmod(path, mode, *a, **kw)

    monkeypatch.setattr(os, "chmod", refusing_chmod)
    target = sock_dir / "refused.sock"

    with caplog.at_level(logging.WARNING, logger="fabric_nemo_sidecar"):
        sock = cli_module.bind_uds(str(target))

    try:
        assert target.exists()
        assert stat.S_ISSOCK(target.stat().st_mode)
        # Prove it is genuinely accepting connections, not merely created.
        client = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            client.connect(str(target))
        finally:
            client.close()
    finally:
        sock.close()

    warnings = " ".join(
        r.getMessage().lower() for r in caplog.records if r.levelno == logging.WARNING
    )
    assert "permission" in warnings
    assert str(target) in warnings or "chmod" in warnings


def test_cli_rails_config_requires_nemoguardrails(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # nemoguardrails is NOT in the dev extras, so the import fails
    # before uvicorn is reached. This proves the --rails-config path
    # eagerly validates the extra at startup.
    monkeypatch.setattr(UVICORN_RUN, lambda **kw: None)
    with pytest.raises(ImportError):
        cli_module.main(["--port", "8082", "--rails-config", str(tmp_path)])
