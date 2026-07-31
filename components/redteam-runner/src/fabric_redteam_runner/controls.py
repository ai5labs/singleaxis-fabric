# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
"""Control catalog — the red-team ⋈ production join key (spec 024 §4).

The join an auditor actually wants is *"we tested for injection
pre-launch"* ⋈ *"the injection rail fired in production"*. It cannot be
made on the names either side already carries: the test side names a
**probe** (``garak:promptinject.PromptInject``) and the production side
names a **vendor rail** (``nemo.self_check_input``). Neither is stable
across a vendor swap and neither is shared.

This module introduces the indirection that fixes it. A **control** is
a named defensive capability — ``injection.input``. ``nemo.self_check_input``
and ``lakera.prompt_injection`` are *implementations* of it;
``garak:promptinject.PromptInject`` is a *test* of it. Both sides
resolve to the same ``control.id`` through this catalog, so replacing a
vendor leaves the audit trail intact.

Two invariants make the join defensible rather than decorative:

* ``catalog_version`` is stamped on both sides. A remap bumps it, which
  makes a cross-version join *fail* instead of silently re-pointing old
  evidence at a new definition.
* An unresolvable probe or rail is reported as ``unmapped``, never
  dropped. The negative space — a rail that fires with no probe testing
  it, a probe that maps to no deployed rail — is the finding, and it is
  only obtainable if both halves stay queryable.
"""

from __future__ import annotations

import logging
from collections.abc import Iterator, Sequence
from importlib import resources
from pathlib import Path
from typing import Annotated

import yaml
from pydantic import BaseModel, ConfigDict, Field, StringConstraints

_LOG = logging.getLogger(__name__)

#: Control ids are lowercase-only and dot-namespaced so that plain
#: string equality is a valid join predicate in SQL, PromQL, or a
#: dashboard filter. No case folding, no normalisation step, no chance
#: of ``Injection.Input`` and ``injection.input`` counting separately.
ControlId = Annotated[
    str,
    StringConstraints(pattern=r"^[a-z0-9]+(\.[a-z0-9_]+)*$", max_length=64),
]

#: Default catalog shipped inside the wheel. Operators override it with
#: ``--controls-catalog`` / ``FABRIC_REDTEAM_CONTROLS_CATALOG`` (in a
#: cluster, a ConfigMap mounted at ``/etc/fabric/controls/catalog.yaml``).
BUNDLED_CATALOG_RESOURCE = "control_catalog.yaml"


class CatalogError(ValueError):
    """The catalog is structurally valid YAML but semantically broken.

    Raised for duplicate control ids and for a probe or implementation
    claimed by more than one control. Both are silent-corruption bugs if
    tolerated: a last-write-wins index would make the join answer depend
    on file ordering."""


class _Base(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True, str_strip_whitespace=True)


class ControlDefinition(_Base):
    """One defensive capability, plus the two id spaces that alias it."""

    id: ControlId
    title: str
    #: Regulatory / framework references. Carried once here rather than
    #: stamped onto every span: one control maps to many frameworks and
    #: this is static metadata, not per-event data.
    frameworks: tuple[str, ...] = ()
    #: Production-side ids — vendor rail / policy names as they appear in
    #: ``fabric.guardrail.policies`` and ``GuardrailSummary.rail_id``.
    implementations: tuple[str, ...] = ()
    #: Test-side ids, each ``"<suite>:<probe>"``.
    probes: tuple[str, ...] = ()


class _CatalogFile(_Base):
    """On-disk shape. Parsed, then handed to :class:`ControlCatalog`,
    which owns the cross-control uniqueness checks."""

    catalog_version: str
    controls: tuple[ControlDefinition, ...] = Field(default_factory=tuple)


def probe_key(suite: str, probe: str) -> str:
    """Canonical test-side id. ``suite`` alone is ambiguous — garak and
    pyrit both ship a ``prompt_injection``-ish probe."""

    return f"{suite}:{probe}"


class ControlCatalog:
    """Resolved catalog with both lookup indexes built and checked.

    Deliberately not a pydantic model: the indexes are derived state and
    the uniqueness rules span controls, so construction is the natural
    place to enforce them.
    """

    def __init__(
        self,
        *,
        catalog_version: str,
        controls: Sequence[ControlDefinition],
    ) -> None:
        self.catalog_version = catalog_version
        self.controls: tuple[ControlDefinition, ...] = tuple(controls)

        self._by_id: dict[str, ControlDefinition] = {}
        for control in self.controls:
            if control.id in self._by_id:
                raise CatalogError(f"duplicate control id {control.id!r}")
            self._by_id[control.id] = control

        self._probe_index = self._build_index("probes")
        self._implementation_index = self._build_index("implementations")

    def _build_index(self, field: str) -> dict[str, str]:
        index: dict[str, str] = {}
        for control in self.controls:
            for key in getattr(control, field):
                owner = index.get(key)
                if owner is not None and owner != control.id:
                    raise CatalogError(
                        f"{field[:-1]} {key!r} is claimed by both "
                        f"{owner!r} and {control.id!r}; a mapping must resolve "
                        "to exactly one control"
                    )
                index[key] = control.id
        return index

    # --- lookups --------------------------------------------------------

    def control_for_probe(self, suite: str, probe: str) -> str | None:
        """Test side: ``(suite, probe)`` → control id, or ``None`` when
        the probe is not in the catalog. ``None`` is a coverage finding,
        not an error — see ``fabric.control.unmapped``."""

        return self._probe_index.get(probe_key(suite, probe))

    def control_for_implementation(self, implementation: str) -> str | None:
        """Production side: a fired rail / policy id → control id.

        Phase 1 resolves the production side at *read* time (evaluation
        ingest, Admin UI, decision-graph), which is why this lives in the
        shared loader rather than only in the runner. The SDK stamping
        ``fabric.control.ids`` at emit time is Phase 2 and does not
        change this contract."""

        return self._implementation_index.get(implementation)

    def get(self, control_id: str) -> ControlDefinition | None:
        return self._by_id.get(control_id)

    def __len__(self) -> int:
        return len(self.controls)

    def __iter__(self) -> Iterator[ControlDefinition]:
        return iter(self.controls)


def parse_catalog(raw: object) -> ControlCatalog:
    """Validate an already-deserialized catalog mapping."""

    if not isinstance(raw, dict):
        raise CatalogError("control catalog: top-level YAML must be a mapping")
    parsed = _CatalogFile.model_validate(raw)
    return ControlCatalog(
        catalog_version=parsed.catalog_version,
        controls=parsed.controls,
    )


def load_catalog(path: Path | None = None) -> ControlCatalog:
    """Load the catalog from ``path``, or the bundled default.

    A missing override path is a hard error: silently falling back to the
    bundled catalog would let an operator believe their custom mapping is
    in force when it is not, and every joined result would be wrong under
    a ``catalog_version`` that claims otherwise."""

    if path is None:
        text = (
            resources.files("fabric_redteam_runner")
            .joinpath(BUNDLED_CATALOG_RESOURCE)
            .read_text(encoding="utf-8")
        )
    else:
        if not Path(path).is_file():
            raise CatalogError(f"control catalog not found: {path}")
        text = Path(path).read_text(encoding="utf-8")
    return parse_catalog(yaml.safe_load(text))
