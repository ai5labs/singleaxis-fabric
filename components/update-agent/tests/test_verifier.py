# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
from __future__ import annotations

from typing import Any

import yaml

from fabric_update_agent.config import VerifierConfig
from fabric_update_agent.signatures import SIGNATURE_ANNOTATION
from fabric_update_agent.verifier import Verifier
from fabric_update_agent.version import VERSION_CONSTRAINT_ANNOTATION


def test_happy_path_allows(signed_configmap: dict[str, Any], config: VerifierConfig) -> None:
    result = Verifier(config).verify(signed_configmap)
    assert result.allowed
    assert result.signer_id == "singleaxis-release"
    assert result.reason is None


def test_tampered_body_denies(signed_configmap: dict[str, Any], config: VerifierConfig) -> None:
    signed_configmap["data"]["bundle.yaml"] = "tampered"
    result = Verifier(config).verify(signed_configmap)
    assert not result.allowed
    assert "did not verify" in (result.reason or "")


def test_wrong_cluster_version_denies(
    signed_configmap: dict[str, Any], public_key_b64: str, sign: Any
) -> None:
    config = VerifierConfig(
        fabric_version="0.5.0",
        trusted_keys=[{"id": "singleaxis-release", "public_key": public_key_b64}],
    )
    result = Verifier(config).verify(signed_configmap)
    assert not result.allowed
    assert "does not satisfy" in (result.reason or "")


def test_missing_annotations_fail_closed(config: VerifierConfig) -> None:
    result = Verifier(config).verify(
        {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "off-path"}}
    )
    assert not result.allowed
    assert "fail_closed" in (result.reason or "")


def test_missing_annotations_fail_open_allows(public_key_b64: str) -> None:
    config = VerifierConfig(
        fabric_version="0.1.0",
        trusted_keys=[{"id": "singleaxis-release", "public_key": public_key_b64}],
        fail_closed=False,
    )
    result = Verifier(config).verify(
        {"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "off-path"}}
    )
    assert result.allowed


def test_partial_annotations_always_deny(
    signed_configmap: dict[str, Any], config: VerifierConfig
) -> None:
    # Drop the signature but keep the version constraint.
    del signed_configmap["metadata"]["annotations"][SIGNATURE_ANNOTATION]
    r = Verifier(config).verify(signed_configmap)
    assert not r.allowed and SIGNATURE_ANNOTATION in (r.reason or "")

    signed_configmap["metadata"]["annotations"][SIGNATURE_ANNOTATION] = "release:AAAA"
    del signed_configmap["metadata"]["annotations"][VERSION_CONSTRAINT_ANNOTATION]
    r = Verifier(config).verify(signed_configmap)
    assert not r.allowed and VERSION_CONSTRAINT_ANNOTATION in (r.reason or "")


# --- Profile-lock admission backstop ---


def _locked_config() -> VerifierConfig:
    return VerifierConfig(fabric_version="0.7.0", enforce_locked_fields=True)


def test_backstop_off_by_default(locked_config: dict[str, Any]) -> None:
    config = VerifierConfig(fabric_version="0.7.0", fail_closed=False)
    assert config.enforce_locked_fields is False
    locked_config["data"]["config.yaml"] = "processors: {}"
    # Without the backstop an unannotated direct edit sails through.
    assert Verifier(config).verify(locked_config).allowed


def test_backstop_allows_intact_config(
    locked_config: dict[str, Any], public_key_b64: str, sign: Any
) -> None:
    config = VerifierConfig(
        fabric_version="0.7.0",
        trusted_keys=[{"id": "singleaxis-release", "public_key": public_key_b64}],
        enforce_locked_fields=True,
    )
    locked_config["metadata"]["annotations"] = {VERSION_CONSTRAINT_ANNOTATION: ">=0.7,<0.8"}
    locked_config["metadata"]["annotations"][SIGNATURE_ANNOTATION] = sign(locked_config)
    r = Verifier(config).verify(locked_config)
    assert r.allowed and r.signer_id == "singleaxis-release"


def test_backstop_denies_missing_guard(locked_config: dict[str, Any]) -> None:
    locked_config["data"]["config.yaml"] = "processors:\n  batch: {}\n"
    r = Verifier(_locked_config()).verify(locked_config)
    assert not r.allowed
    assert "fabric.guard.enabled" in (r.reason or "")


def test_backstop_denies_drop_unknown_classes_false(
    locked_config_factory: Any,
) -> None:
    cm = locked_config_factory(drop_unknown_classes=False)
    r = Verifier(_locked_config()).verify(cm)
    assert not r.allowed
    assert "dropUnknownClasses" in (r.reason or "")


def test_backstop_denies_missing_redact(locked_config: dict[str, Any]) -> None:
    locked_config["data"]["config.yaml"] = (
        "processors:\n  fabricguard:\n    drop_unknown_classes: true\n"
    )
    r = Verifier(_locked_config()).verify(locked_config)
    assert not r.allowed
    assert "fabric.redact.enabled" in (r.reason or "")


def test_backstop_denies_declared_but_inactive_processors(
    locked_config: dict[str, Any],
) -> None:
    rendered = yaml.safe_load(locked_config["data"]["config.yaml"])
    rendered["service"]["pipelines"]["logs"]["processors"] = ["memory_limiter", "batch"]
    locked_config["data"]["config.yaml"] = yaml.safe_dump(rendered)
    r = Verifier(_locked_config()).verify(locked_config)
    assert not r.allowed
    assert "fabricguard is not chained into the logs pipeline" in (r.reason or "")


def test_backstop_denies_missing_traces_pipeline(locked_config: dict[str, Any]) -> None:
    rendered = yaml.safe_load(locked_config["data"]["config.yaml"])
    del rendered["service"]["pipelines"]["traces"]
    locked_config["data"]["config.yaml"] = yaml.safe_dump(rendered)
    r = Verifier(_locked_config()).verify(locked_config)
    assert not r.allowed
    assert "no active traces pipeline" in (r.reason or "")


def test_backstop_fails_closed_on_unparseable(locked_config: dict[str, Any]) -> None:
    locked_config["data"]["config.yaml"] = "processors: [unclosed"
    r = Verifier(_locked_config()).verify(locked_config)
    assert not r.allowed and "does not parse" in (r.reason or "")


def test_backstop_fails_closed_on_missing_data(locked_config: dict[str, Any]) -> None:
    del locked_config["data"]["config.yaml"]
    r = Verifier(_locked_config()).verify(locked_config)
    assert not r.allowed and "cannot be confirmed" in (r.reason or "")


def test_backstop_matches_name_suffix_without_labels(
    locked_config_factory: Any,
) -> None:
    cm = locked_config_factory(name="acme-otel-collector-config", labels={})
    cm["data"]["config.yaml"] = "processors: {}"
    r = Verifier(_locked_config()).verify(cm)
    assert not r.allowed


def test_backstop_ignores_unrelated_configmaps(config: VerifierConfig) -> None:
    cm = {
        "apiVersion": "v1",
        "kind": "ConfigMap",
        "metadata": {"name": "unrelated", "labels": {"app": "other"}},
        "data": {"config.yaml": "processors: {}"},
    }
    assert Verifier(config).verify(cm).allowed is False  # fail_closed, not backstop
    assert "fabricguard" not in (Verifier(config).verify(cm).reason or "")
