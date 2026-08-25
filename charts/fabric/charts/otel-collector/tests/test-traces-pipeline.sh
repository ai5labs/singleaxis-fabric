#!/usr/bin/env bash
# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
#
# Asserts the otel-collector chart renders the OTLP `traces:` pipeline
# when `fabric.guard.traceProcessingEnabled` is true (the default) and
# omits it when explicitly disabled.
#
# The SDK ships trace spans; without this pipeline the collector
# returns 404 on `/v1/traces` and the chart silently drops them on the
# floor.
#
# Requires: helm 3, bash. Run from repo root or from this directory.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
chart_dir="$(cd "${here}/.." && pwd)"

# Common values. The sampler is off by default (no key to invent for a
# bare install), so turn it on with a key to render the production-shape
# pipeline. The exporter endpoint has no OSS default; set one so the
# `otlphttp/fabric` exporter is rendered — with it empty the chart falls
# back to the `debug` exporter instead, which is a different assertion.
common_args=(
  --set fabric.sampler.enabled=true
  --set fabric.sampler.hmacKey=00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff
  --set exporter.endpoint=http://otlp.example:4318
)

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

pass() {
  echo "ok: $*"
}

# Case 1: default render must contain a `traces:` pipeline block.
default_render=$(helm template ci "${chart_dir}" "${common_args[@]}")
if ! grep -qE '^[[:space:]]+traces:[[:space:]]*$' <<<"${default_render}"; then
  fail "default render is missing the 'traces:' pipeline block"
fi
pass "default render contains traces: pipeline"

# Case 2: traces pipeline chains the same processors as logs — guard,
# then any enabled Fabric processors, then batch. With the common_args
# above (sampler on; redact/policy off by default) that is exactly:
#   memory_limiter, fabricguard, fabricsampler, batch.
expected_processors=$'processors:\n            - memory_limiter\n            - fabricguard\n            - fabricsampler\n            - batch'
if ! grep -q -F -- "${expected_processors}" <<<"${default_render}"; then
  fail "traces pipeline processors don't match the expected chain (memory_limiter, fabricguard, fabricsampler, batch)"
fi
if ! grep -qE 'exporters:[[:space:]]*\[otlphttp/fabric\]' <<<"${default_render}"; then
  fail "traces pipeline exporters don't match the expected [otlphttp/fabric]"
fi
pass "traces pipeline has correct processors + exporters"

# Case 3: opt-out via traceProcessingEnabled=false drops the block.
disabled_render=$(helm template ci "${chart_dir}" \
  "${common_args[@]}" \
  --set fabric.guard.traceProcessingEnabled=false)
if grep -qE '^[[:space:]]+traces:[[:space:]]*$' <<<"${disabled_render}"; then
  fail "traces: pipeline still rendered when fabric.guard.traceProcessingEnabled=false"
fi
pass "traces: pipeline omitted when disabled"

# Case 4: the existing logs: pipeline is unaffected by the new gate.
if ! grep -qE '^[[:space:]]+logs:[[:space:]]*$' <<<"${default_render}"; then
  fail "regression: logs: pipeline missing from default render"
fi
if ! grep -qE '^[[:space:]]+logs:[[:space:]]*$' <<<"${disabled_render}"; then
  fail "regression: logs: pipeline missing when traces are disabled"
fi
pass "logs: pipeline present in both renders"

# Case 5 (H-1): enabling redact/policy must chain them into the TRACES
# pipeline too, not just logs. Use the production-shaped co-located
# provider; enabled redaction no longer has a missing-provider bypass.
redact_render=$(helm template ci "${chart_dir}" \
  "${common_args[@]}" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.provider.mode=sidecar \
  --set fabric.redact.provider.sidecar.tenantKeySecret.name=fabric-presidio-tenant-key)
traces_block=$(awk '/^        traces:/{f=1} f&&/^        [a-z]+:$/&&$0!~/^        traces:/{f=0} f' <<<"${redact_render}")
if ! grep -q -- "- fabricredact" <<<"${traces_block}"; then
  fail "fabricredact not chained into the traces pipeline (H-1 regression)"
fi
pass "fabricredact chained into traces pipeline"

policy_render=$(helm template ci "${chart_dir}" \
  "${common_args[@]}" \
  --set fabric.policy.enabled=true \
  --set fabric.policy.bundlePath=/etc/fabric/policy \
  --set fabric.policy.bundleConfigMap=fabric-policy-bundle)
traces_block=$(awk '/^        traces:/{f=1} f&&/^        [a-z]+:$/&&$0!~/^        traces:/{f=0} f' <<<"${policy_render}")
if ! grep -q -- "- fabricpolicy" <<<"${traces_block}"; then
  fail "fabricpolicy not chained into the traces pipeline (H-1 regression)"
fi
if ! grep -q "name: fabric-policy-bundle" <<<"${policy_render}"; then
  fail "bundleConfigMap did not render a configMap volume source"
fi
pass "fabricpolicy + bundleConfigMap rendered in traces pipeline"

echo "all checks passed"
