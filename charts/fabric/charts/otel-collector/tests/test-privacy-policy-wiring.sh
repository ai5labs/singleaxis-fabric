#!/usr/bin/env bash
# Render contract for the Collector's privacy and policy wiring.
# Requires Helm 3. Run from the repository root or this directory.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
chart_dir="$(cd "${here}/.." && pwd)"
umbrella_dir="$(cd "${chart_dir}/../.." && pwd)"
high_risk_profile="${umbrella_dir}/profiles/eu-ai-act-high-risk.yaml"

pass_count=0
fail_count=0

pass() { printf 'ok: %s\n' "$*"; pass_count=$((pass_count + 1)); }
fail() { printf 'FAIL: %s\n' "$*" >&2; fail_count=$((fail_count + 1)); }

expect_fail() {
  local label="$1" needle="$2"
  shift 2
  local output
  if output="$(helm template ci "${chart_dir}" "$@" 2>&1)"; then
    fail "${label}: render unexpectedly succeeded"
  elif [[ "${output}" != *"${needle}"* ]]; then
    fail "${label}: expected error containing '${needle}', got: ${output}"
  else
    pass "${label}"
  fi
}

expect_fail_either() {
  local label="$1" template_needle="$2" schema_needle="$3"
  shift 3
  local output
  if output="$(helm template ci "${chart_dir}" "$@" 2>&1)"; then
    fail "${label}: render unexpectedly succeeded"
  elif [[ "${output}" != *"${template_needle}"* && "${output}" != *"${schema_needle}"* ]]; then
    fail "${label}: expected error containing '${template_needle}' or '${schema_needle}', got: ${output}"
  else
    pass "${label}"
  fi
}

expect_contains() {
  local label="$1" haystack="$2" needle="$3"
  if grep -q -F -- "${needle}" <<<"${haystack}"; then
    pass "${label}"
  else
    fail "${label}: rendered manifest lacks '${needle}'"
  fi
}

expect_not_contains() {
  local label="$1" haystack="$2" needle="$3"
  if grep -q -F -- "${needle}" <<<"${haystack}"; then
    fail "${label}: rendered manifest unexpectedly contains '${needle}'"
  else
    pass "${label}"
  fi
}

printf '\n=== Presidio UDS provider validation ===\n'
expect_fail \
  "redaction requires embedded provider" \
  "requires fabric.redact.embedded.enabled=true" \
  --set fabric.redact.enabled=true

expect_fail_either \
  "provider-name acknowledgement cannot bypass wiring" \
  "requires fabric.redact.embedded.enabled=true" \
  "existingSocketProvider" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.existingSocketProvider=pretend-provider

expect_fail \
  "embedded provider requires tenant key Secret" \
  "requires fabric.redact.embedded.tenantKeySecret.name" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.embedded.enabled=true

expect_fail \
  "embedded provider cannot run unused" \
  "requires fabric.redact.enabled=true" \
  --set fabric.redact.embedded.enabled=true \
  --set fabric.redact.embedded.tenantKeySecret.name=fabric-presidio-key

expect_fail_either \
  "redaction mode is validated" \
  "redactionMode must be one of" \
  "value must be one of 'hmac', 'tag'" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.embedded.enabled=true \
  --set fabric.redact.embedded.tenantKeySecret.name=fabric-presidio-key \
  --set fabric.redact.embedded.redactionMode=unknown

expect_fail_either \
  "byte handling is validated" \
  "byteHandling must be one of" \
  "value must be one of 'redact_utf8', 'reject', 'passthrough'" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.embedded.enabled=true \
  --set fabric.redact.embedded.tenantKeySecret.name=fabric-presidio-key \
  --set fabric.redact.byteHandling=base64

for unsafe_socket_case in \
  "/presidio.sock|non-root absolute directory" \
  "/var/run/fabric/../presidio.sock|must already be normalized" \
  "/var//run/fabric/presidio.sock|must already be normalized" \
  "/var/run/fabric/|must already be normalized"; do
  unsafe_socket="${unsafe_socket_case%%|*}"
  expected_error="${unsafe_socket_case#*|}"
  expect_fail \
    "unsafe socket path ${unsafe_socket} is rejected" \
    "${expected_error}" \
    --set fabric.redact.enabled=true \
    --set fabric.redact.embedded.enabled=true \
    --set fabric.redact.embedded.tenantKeySecret.name=fabric-presidio-key \
    --set-string "fabric.redact.unixSocket=${unsafe_socket}"
done

redact_render="$(helm template ci "${chart_dir}" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.embedded.enabled=true \
  --set fabric.redact.embedded.tenantKeySecret.name=fabric-presidio-key)"
expect_contains "embedded Presidio container renders" "${redact_render}" "name: presidio-redactor"
expect_contains "secure byte handling renders" "${redact_render}" 'byte_handling: "redact_utf8"'
expect_contains "sidecar binds configured UDS" "${redact_render}" "- \"/var/run/fabric/presidio.sock\""
expect_contains "shared socket volume renders" "${redact_render}" "name: redaction-socket"
expect_contains "tenant key Secret is mounted" "${redact_render}" "secretName: fabric-presidio-key"
expect_contains "socket startup/readiness probe checks health" "${redact_render}" "GET /healthz HTTP/1.1"
expect_contains "collector and sidecar use socket-compatible UID" "${redact_render}" "runAsUser: 65532"

printf '\n=== Policy source validation ===\n'
expect_fail \
  "bundle path cannot mount an empty directory" \
  "no policy source exists" \
  --set fabric.policy.bundlePath=/etc/fabric/policy

expect_fail \
  "policy sources are exclusive" \
  "mutually exclusive" \
  --set fabric.policy.bundlePath=/etc/fabric/policy \
  --set fabric.policy.bundleConfigMap=tenant-policy \
  --set fabric.policy.referencePolicy.enabled=true

expect_fail_either \
  "reference policy version is pinned" \
  "currently ships only v1" \
  "must be" \
  --set fabric.policy.bundlePath=/etc/fabric/policy \
  --set fabric.policy.referencePolicy.enabled=true \
  --set fabric.policy.referencePolicy.version=v2

external_policy_render="$(helm template ci "${chart_dir}" \
  --set fabric.policy.bundlePath=/etc/fabric/policy \
  --set fabric.policy.bundleConfigMap=tenant-policy)"
expect_contains "external policy ConfigMap mounts" "${external_policy_render}" "name: tenant-policy"
expect_not_contains "policy volume never falls back to emptyDir" "${external_policy_render}" "No in-chart provisioning mechanism"

reference_policy_render="$(helm template ci "${chart_dir}" \
  --set fabric.policy.bundlePath=/etc/fabric/policy \
  --set fabric.policy.referencePolicy.enabled=true)"
expect_contains "versioned reference policy ConfigMap renders" "${reference_policy_render}" "name: ci-otel-collector-reference-policy-v1"
expect_contains "reference policy is immutable" "${reference_policy_render}" "immutable: true"
expect_contains "reference policy states limitation" "${reference_policy_render}" "not tenant authorization"
expect_contains "reference policy requires tenant id" "${reference_policy_render}" 'input.attributes.tenant_id != ""'

printf '\n=== High-risk profile integration ===\n'
high_risk_render="$(helm template ci "${umbrella_dir}" \
  --values "${high_risk_profile}" \
  --set tenant.id=11111111-1111-4111-8111-111111111111 \
  --set otel-collector.exporter.endpoint=https://otlp.example:4318 \
  --set 'otel-collector.networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32' \
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].protocol=TCP' \
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].port=4318' \
  --set update-agent.config.allowPlaceholderKey=true)"
expect_contains "profile renders embedded Presidio" "${high_risk_render}" "name: presidio-redactor"
expect_contains "profile references tenant redaction key" "${high_risk_render}" "secretName: fabric-presidio-tenant-key"
expect_contains "profile renders reference policy" "${high_risk_render}" "ci-otel-collector-reference-policy-v1"

if helm template ci "${umbrella_dir}" \
  --values "${high_risk_profile}" \
  --set tenant.id=11111111-1111-4111-8111-111111111111 \
  --set otel-collector.exporter.endpoint=https://otlp.example:4318 \
  --set otel-collector.fabric.redact.embedded.enabled=false \
  --set 'otel-collector.networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32' \
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].protocol=TCP' \
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].port=4318' \
  --set update-agent.config.allowPlaceholderKey=true >/dev/null 2>&1; then
  fail "high-risk profile: disabling embedded provider should fail render"
else
  pass "high-risk profile cannot render without embedded provider"
fi

printf '\n--- summary: %d passed, %d failed ---\n' "${pass_count}" "${fail_count}"
exit $((fail_count > 0 ? 1 : 0))
