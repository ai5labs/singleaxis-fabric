#!/usr/bin/env python3
# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Controlled OTLP/HTTP fsync sink used only by Fabric end-to-end tests."""

from __future__ import annotations

import base64
import gzip
import json
import os
import uuid
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlparse

DATA_DIR = Path(os.environ.get("SINK_DATA_DIR", "/var/lib/fabric-test-sink"))
PORT = int(os.environ.get("SINK_PORT", "8080"))
DATA_DIR.mkdir(parents=True, exist_ok=True)


def records() -> list[Path]:
    return sorted(
        DATA_DIR.glob("*.otlp"), key=lambda path: (path.stat().st_mtime_ns, path.name)
    )


def non_negative_int(query: dict[str, list[str]], key: str, default: int) -> int:
    try:
        value = int(query.get(key, [str(default)])[0])
    except ValueError as exc:
        raise ValueError(f"{key} must be a non-negative integer") from exc
    if value < 0:
        raise ValueError(f"{key} must be a non-negative integer")
    return value


class Handler(BaseHTTPRequestHandler):
    server_version = "FabricE2ETestSink/1"

    def _json(self, status: HTTPStatus, value: object) -> None:
        body = json.dumps(value, sort_keys=True).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802
        parsed = urlparse(self.path)
        if parsed.path == "/health":
            self._json(HTTPStatus.OK, {"status": "ready"})
            return
        if parsed.path == "/count":
            self._json(HTTPStatus.OK, {"count": len(records())})
            return
        if parsed.path == "/contains":
            query = parse_qs(parsed.query)
            needle = query.get("needle", [""])[0].encode()
            try:
                after = non_negative_int(query, "after", 0)
            except ValueError as exc:
                self._json(HTTPStatus.BAD_REQUEST, {"error": str(exc)})
                return
            found = bool(needle) and any(
                needle in path.read_bytes() for path in records()[after:]
            )
            self._json(HTTPStatus.OK, {"found": found})
            return
        if parsed.path == "/records":
            query = parse_qs(parsed.query)
            try:
                after = non_negative_int(query, "after", 0)
                limit = non_negative_int(query, "limit", 200)
            except ValueError as exc:
                self._json(HTTPStatus.BAD_REQUEST, {"error": str(exc)})
                return
            if limit < 1 or limit > 200:
                self._json(
                    HTTPStatus.BAD_REQUEST,
                    {"error": "limit must be between 1 and 200"},
                )
                return
            selected = records()[after : after + limit]
            self._json(
                HTTPStatus.OK,
                {
                    "after": after,
                    "records": [
                        {
                            "index": after + offset,
                            "body_base64": base64.b64encode(path.read_bytes()).decode(),
                        }
                        for offset, path in enumerate(selected)
                    ],
                    "total": len(records()),
                },
            )
            return
        self._json(HTTPStatus.NOT_FOUND, {"error": "not found"})

    def do_POST(self) -> None:  # noqa: N802
        if self.path not in {"/v1/traces", "/v1/logs", "/v1/metrics"}:
            self._json(HTTPStatus.NOT_FOUND, {"error": "not an OTLP/HTTP path"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        if self.headers.get("Content-Encoding", "").lower() == "gzip":
            body = gzip.decompress(body)

        record_id = uuid.uuid4().hex
        temporary = DATA_DIR / f".{record_id}.tmp"
        final = DATA_DIR / f"{record_id}.otlp"
        with temporary.open("wb") as stream:
            stream.write(body)
            stream.flush()
            os.fsync(stream.fileno())
        temporary.replace(final)
        directory_fd = os.open(DATA_DIR, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)

        # The empty protobuf ExportResponse is valid OTLP/HTTP. Success means
        # this test sink fsynced the request; that guarantee is sink-specific.
        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Type", "application/x-protobuf")
        self.send_header("Content-Length", "0")
        self.send_header("X-Fabric-Test-Receipt", record_id)
        self.end_headers()

    def log_message(self, format: str, *args: object) -> None:
        print(f"test-sink {self.address_string()} {format % args}", flush=True)


ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
