# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Exact equivalence guard for the canonical and Helm starter bundles.

The component source is authoritative. Helm must package files inside the
chart, so it carries generated mirrors and injects them with ``.Files.Get``.
These tests reject byte drift in the packaged mirrors and content drift in
the rendered ConfigMap, including extra stale Colang flows.
"""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path

import pytest

_REPO_ROOT = Path(__file__).resolve().parents[3]
_CANONICAL = _REPO_ROOT / "components" / "nemo-sidecar" / "rails" / "starter"
_CHART = _REPO_ROOT / "charts" / "fabric" / "charts" / "nemo-sidecar"
_PACKAGED = _CHART / "files" / "rails" / "starter"
_FILENAMES = ("config.yml", "rails.co")


def _render_chart(*extra_args: str) -> str:
    helm = shutil.which("helm")
    if helm is None:
        pytest.skip("helm is required for rendered chart equivalence")
    result = subprocess.run(  # noqa: S603
        [helm, "template", "starter-test", str(_CHART), *extra_args],
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout


def _parse_configmap_data(rendered: str) -> dict[str, str]:
    """Parse literal block values from the rendered starter ConfigMap.

    The chart owns exactly two ConfigMap data keys and renders both as YAML
    literal blocks. Parsing that constrained shape here avoids adding a YAML
    runtime dependency solely for a packaging invariant while still comparing
    the values Kubernetes receives, rather than searching template text.
    """

    documents = rendered.split("\n---\n")
    document = next(
        doc for doc in documents if "kind: ConfigMap" in doc and "-rails-starter" in doc
    )
    lines = document.splitlines(keepends=True)
    data_index = lines.index("data:\n")
    parsed: dict[str, str] = {}
    index = data_index + 1
    while index < len(lines):
        line = lines[index]
        if not line.startswith("  "):
            break
        if not line.startswith("  ") or not line.rstrip("\n").endswith(": |"):
            index += 1
            continue
        key = line.strip().removesuffix(": |")
        index += 1
        value_lines: list[str] = []
        while index < len(lines) and lines[index].startswith("    "):
            value_lines.append(lines[index][4:])
            index += 1
        # YAML's default ``|`` chomping keeps exactly one final newline,
        # regardless of how many line breaks precede the next key (or EOF).
        parsed[key] = "".join(value_lines).rstrip("\n") + "\n"
    return parsed


@pytest.mark.parametrize("filename", _FILENAMES)
def test_packaged_starter_file_is_byte_identical_to_canonical_source(filename: str) -> None:
    assert (_PACKAGED / filename).read_bytes() == (_CANONICAL / filename).read_bytes(), (
        f"{filename} is stale; run "
        "python components/nemo-sidecar/tools/sync_starter_chart_bundle.py"
    )


def test_rendered_configmap_exactly_matches_canonical_bundle() -> None:
    rendered_data = _parse_configmap_data(_render_chart())
    expected = {name: (_CANONICAL / name).read_text(encoding="utf-8") for name in _FILENAMES}
    assert rendered_data == expected


def test_rendered_starter_explicitly_enables_default_literal_filter() -> None:
    rendered = _render_chart("--show-only", "templates/deployment.yaml")
    assert '- "--rails-config"' in rendered
    assert '- "--starter-literal-only"' in rendered
    assert '- "--enable-default-literal-filter"' in rendered
    assert "--allow-passthrough" not in rendered


def test_custom_configmap_selects_nemo_not_starter_mode() -> None:
    rendered = _render_chart(
        "--show-only",
        "templates/deployment.yaml",
        "--set",
        "railsConfigMap.name=tenant-rails",
    )
    assert '- "--rails-config"' in rendered
    assert '- "--enable-default-literal-filter"' in rendered
    assert "--starter-literal-only" not in rendered
    assert "--allow-passthrough" not in rendered


def test_explicit_passthrough_selects_no_filter_or_nemo_mode() -> None:
    rendered = _render_chart(
        "--show-only",
        "templates/deployment.yaml",
        "--set",
        "starterRails.enabled=false",
        "--set",
        "allowPassthrough=true",
    )
    assert '- "--allow-passthrough"' in rendered
    assert "--rails-config" not in rendered
    assert "--starter-literal-only" not in rendered
    assert "--enable-default-literal-filter" not in rendered


def test_chart_rejects_starter_without_literal_filter() -> None:
    helm = shutil.which("helm")
    if helm is None:
        pytest.skip("helm is required for chart validation")
    result = subprocess.run(  # noqa: S603
        [
            helm,
            "template",
            "unsafe-starter",
            str(_CHART),
            "--set",
            "literalFilter.enabled=false",
        ],
        check=False,
        capture_output=True,
        text=True,
    )
    assert result.returncode != 0
    assert "literalFilter" in result.stderr
    assert "true" in result.stderr or "requires literalFilter.enabled" in result.stderr
