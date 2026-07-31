# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0

from __future__ import annotations

import errno
import logging
import os
import shutil
import socket
import stat
import sys
import tempfile
import types
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from fabric_prompt_guard_sidecar import __main__ as cli_module

UVICORN_RUN = "fabric_prompt_guard_sidecar.__main__.uvicorn.run"


@pytest.fixture
def sock_dir() -> Iterator[Path]:
    """A directory short enough to hold an AF_UNIX path.

    ``tmp_path`` nests test names several levels deep, which overruns the
    ~104-byte ``sockaddr_un.sun_path`` limit on macOS and BSD. Socket
    paths need their own shallow directory.
    """

    path = Path(tempfile.mkdtemp(prefix="fpgs-"))
    try:
        yield path
    finally:
        shutil.rmtree(path, ignore_errors=True)


def test_cli_requires_uds_or_port() -> None:
    with pytest.raises(SystemExit):
        cli_module.main(["--allow-passthrough"])


def test_cli_rejects_both_uds_and_port(tmp_path: Path) -> None:
    with pytest.raises(SystemExit):
        cli_module.main(
            [
                "--uds",
                str(tmp_path / "s.sock"),
                "--port",
                "8080",
                "--allow-passthrough",
            ]
        )


def test_cli_rejects_out_of_range_threshold() -> None:
    with pytest.raises(SystemExit):
        cli_module.main(["--port", "8080", "--allow-passthrough", "--threshold", "1.5"])


def test_cli_invokes_uvicorn_on_uds(sock_dir: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    """The CLI binds the UDS itself and hands uvicorn the descriptor.

    We pass ``fd=`` rather than ``uds=`` so that uvicorn never runs its
    unconditional ``os.chmod`` on the socket — see ``bind_uds``. The
    socket must still be created, bound and listening at the requested
    path, which is what a caller actually depends on.
    """

    captured: dict[str, Any] = {}

    def fake_run(**kwargs: Any) -> None:
        captured.update(kwargs)

    monkeypatch.setattr(UVICORN_RUN, fake_run)
    sock = sock_dir / "sidecar.sock"
    assert cli_module.main(["--uds", str(sock), "--allow-passthrough"]) == 0
    assert "uds" not in captured, "uds= would re-introduce uvicorn's mandatory chmod"
    assert isinstance(captured["fd"], int)
    assert sock.exists()
    assert stat.S_ISSOCK(sock.stat().st_mode), "path must be a real bound socket"
    assert "app" in captured


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

    with caplog.at_level(logging.WARNING, logger="fabric_prompt_guard_sidecar"):
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


def test_cli_fails_loud_without_passthrough_when_extra_missing(
    capsys: pytest.CaptureFixture[str],
) -> None:
    """Without --allow-passthrough, missing [model] extra must abort.

    The dev install does not include the [model] extra, so the import
    inside ``main`` naturally raises ImportError. The fail-loud guard
    must convert that into a parser error mentioning --allow-passthrough
    and the dev / smoke caveat.
    """

    with pytest.raises(SystemExit) as excinfo:
        cli_module.main(["--port", "8090"])
    assert excinfo.value.code == 2
    err = capsys.readouterr().err
    assert "--allow-passthrough" in err
    assert "dev" in err.lower() or "smoke" in err.lower()


def test_cli_passthrough_logs_warning_when_extra_missing(
    monkeypatch: pytest.MonkeyPatch,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """--allow-passthrough lets the sidecar start and logs a warning."""

    monkeypatch.setattr(UVICORN_RUN, lambda **_kw: None)
    with caplog.at_level(logging.WARNING, logger="fabric_prompt_guard_sidecar"):
        rc = cli_module.main(["--port", "8091", "--allow-passthrough"])
    assert rc == 0
    warning_records = [r for r in caplog.records if r.levelno == logging.WARNING]
    assert warning_records, "expected a warning log when passthrough is allowed"
    joined = " ".join(r.getMessage().lower() for r in warning_records)
    assert "passthrough" in joined
    assert "no jailbreak defence" in joined


def test_cli_wires_real_classifier_and_info_logs(
    monkeypatch: pytest.MonkeyPatch,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """When [model] is available, build_default_classifier is called and
    an INFO log records that the real classifier was wired."""

    calls: dict[str, int] = {"build": 0}

    class _FakeClassifier:
        pass

    def _fake_build(model_id: str = "default") -> _FakeClassifier:
        calls["build"] += 1
        return _FakeClassifier()

    # Inject a fake `prompt_guard` module so the lazy import inside
    # ``main`` succeeds without the real [model] extra installed.
    fake_module = types.ModuleType("fabric_prompt_guard_sidecar.prompt_guard")
    fake_module.build_default_classifier = _fake_build  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "fabric_prompt_guard_sidecar.prompt_guard", fake_module)

    captured: dict[str, Any] = {}
    monkeypatch.setattr(UVICORN_RUN, lambda **kw: captured.update(kw))
    with caplog.at_level(logging.INFO, logger="fabric_prompt_guard_sidecar"):
        rc = cli_module.main(["--port", "8092"])
    assert rc == 0
    assert calls["build"] == 1
    info_records = [r for r in caplog.records if r.levelno == logging.INFO]
    joined = " ".join(r.getMessage().lower() for r in info_records)
    assert "real" in joined and "classifier" in joined
    assert "app" in captured


def test_cli_forwards_model_id_when_extra_present(monkeypatch: pytest.MonkeyPatch) -> None:
    """--model-id is forwarded to build_default_classifier."""

    seen: dict[str, str] = {}

    def _fake_build(model_id: str = "default") -> object:
        seen["model_id"] = model_id
        return object()

    fake_module = types.ModuleType("fabric_prompt_guard_sidecar.prompt_guard")
    fake_module.build_default_classifier = _fake_build  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "fabric_prompt_guard_sidecar.prompt_guard", fake_module)
    monkeypatch.setattr(UVICORN_RUN, lambda **_kw: None)

    rc = cli_module.main(["--port", "8093", "--model-id", "meta-llama/Prompt-Guard-86M"])
    assert rc == 0
    assert seen["model_id"] == "meta-llama/Prompt-Guard-86M"
