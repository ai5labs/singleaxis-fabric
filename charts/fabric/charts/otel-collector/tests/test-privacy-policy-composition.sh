#!/usr/bin/env bash
# Copyright 2026 AI5Labs Research OPC Private Limited
# SPDX-License-Identifier: Apache-2.0
#
# Render-level contract tests for the high-risk privacy and policy
# composition. These assertions guard against the two destructive
# historical states: a named-but-unmounted redaction provider and an
# empty policy volume that dropped the entire audit stream.

set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
high_risk_profile="$(cd "${chart_dir}/../.." && pwd)/profiles/eu-ai-act-high-risk.yaml"
pass=0
failures=0

ok() { printf '  PASS: %s\n' "$*"; pass=$((pass + 1)); }
bad() { printf '  FAIL: %s\n' "$*" >&2; failures=$((failures + 1)); }

expect_fail() {
  local label="$1" needle="$2"
  shift 2
  local output
  if output="$(helm template test "${chart_dir}" "$@" 2>&1)"; then
    bad "${label} (render unexpectedly succeeded)"
  elif [[ "${output}" == *"${needle}"* ]]; then
    ok "${label}"
  else
    bad "${label} (expected error containing: ${needle}; got: ${output})"
  fi
}

expect_success() {
  local label="$1"
  shift
  if helm template test "${chart_dir}" "$@" >/dev/null 2>&1; then
    ok "${label}"
  else
    bad "${label} (render failed)"
    helm template test "${chart_dir}" "$@" 2>&1 | sed 's/^/    /' >&2
  fi
}

printf '\n=== Safe defaults ===\n'
expect_success "bare chart renders with optional policy/redaction disabled"

printf '\n=== Redaction provider is concrete ===\n'
expect_fail \
  "redaction without provider mode fails" \
  "provider/mode" \
  --set fabric.redact.enabled=true

expect_fail \
  "legacy provider label cannot satisfy composition" \
  "existingSocketProvider" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.existingSocketProvider=presidio-deployment

expect_fail \
  "sidecar without tenant key Secret fails" \
  "tenantKeySecret/name" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.provider.mode=sidecar

for unsafe_socket_case in \
  "/presidio.sock|non-root absolute directory" \
  "/var/run/fabric/../presidio.sock|must already be normalized" \
  "/var//run/fabric/presidio.sock|must already be normalized" \
  "/var/run/fabric/|must already be normalized"; do
  unsafe_socket="${unsafe_socket_case%%|*}"
  expected_error="${unsafe_socket_case#*|}"
  expect_fail \
    "unsafe socket path ${unsafe_socket} fails" \
    "${expected_error}" \
    --set fabric.redact.enabled=true \
    --set fabric.redact.provider.mode=sidecar \
    --set fabric.redact.provider.sidecar.tenantKeySecret.name=tenant-redaction-key \
    --set-string "fabric.redact.unixSocket=${unsafe_socket}"
done

sidecar_render="$(helm template test "${chart_dir}" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.provider.mode=sidecar \
  --set fabric.redact.provider.sidecar.tenantKeySecret.name=tenant-redaction-key)"

if [[ "$(grep -c 'name: presidio-redactor' <<<"${sidecar_render}")" -eq 1 ]] \
  && grep -q -- '--uds' <<<"${sidecar_render}" \
  && grep -q 'secretName: tenant-redaction-key' <<<"${sidecar_render}" \
  && [[ "$(grep -c 'name: redact-socket' <<<"${sidecar_render}")" -ge 3 ]] \
  && grep -A2 'name: redact-socket' <<<"${sidecar_render}" | grep -q 'emptyDir: {}'; then
  ok "sidecar mode renders container, shared UDS volume, and tenant key"
else
  bad "sidecar mode is missing its container, UDS composition, or tenant key"
fi

expect_fail \
  "external volume mode without VolumeSource fails" \
  "externalVolume/volumeSource" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.provider.mode=externalVolume

expect_fail \
  "external socket volume cannot mount at filesystem root" \
  "externalVolume/mountPath" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.provider.mode=externalVolume \
  --set fabric.redact.provider.externalVolume.mountPath=/ \
  --set fabric.redact.provider.externalVolume.volumeSource.persistentVolumeClaim.claimName=presidio-socket-pvc

external_render="$(helm template test "${chart_dir}" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.provider.mode=externalVolume \
  --set fabric.redact.provider.externalVolume.volumeSource.persistentVolumeClaim.claimName=presidio-socket-pvc)"
if ! grep -q 'name: presidio-redactor' <<<"${external_render}" \
  && grep -q 'claimName: presidio-socket-pvc' <<<"${external_render}" \
  && grep -q 'mountPath: /var/run/fabric' <<<"${external_render}"; then
  ok "external mode renders the declared Kubernetes volume without a fake sidecar"
else
  bad "external mode did not render the declared socket volume correctly"
fi

printf '\n=== Policy bundle is real ===\n'
expect_fail \
  "enabled policy without path fails" \
  "/fabric/policy" \
  --set fabric.policy.enabled=true

expect_fail \
  "enabled policy without ConfigMap fails" \
  "bundleConfigMap" \
  --set fabric.policy.enabled=true \
  --set fabric.policy.bundlePath=/etc/fabric/policy

policy_render="$(helm template test "${chart_dir}" \
  --set fabric.policy.enabled=true \
  --set fabric.policy.bundlePath=/etc/fabric/policy \
  --set fabric.policy.bundleConfigMap=approved-egress-policy)"
policy_volume="$(awk '/name: policy-bundle/{capture=1} capture{print} capture && /name: approved-egress-policy/{exit}' <<<"${policy_render}")"
if grep -q 'name: approved-egress-policy' <<<"${policy_volume}" \
  && ! grep -q 'emptyDir' <<<"${policy_volume}"; then
  ok "policy mounts the named ConfigMap and never an emptyDir"
else
  bad "policy did not mount the named ConfigMap safely"
fi

printf '\n=== Regulated exporter TLS posture ===\n'
profile_exporter_block="$(sed -n '/^  exporter:/,/^  networkPolicy:/p' "${high_risk_profile}")"
if grep -q -- '- otel-collector.exporter.requireEndpoint' "${high_risk_profile}" \
  && grep -q 'requireEndpoint: true' <<<"${profile_exporter_block}" \
  && grep -q 'insecure: false' <<<"${profile_exporter_block}"; then
  ok "high-risk profile pins its positive exporter lock and secure TLS value"
else
  bad "high-risk profile is missing the required endpoint lock or insecure:false"
fi

secure_export_render="$(helm template test "${chart_dir}" \
  --set exporter.requireEndpoint=true \
  --set exporter.endpoint=https://otlp.example.invalid:4318 \
  --set exporter.insecure=false)"
if grep -A3 'tls:' <<<"${secure_export_render}" | grep -q 'insecure: false'; then
  ok "regulated HTTPS exporter renders TLS verification enabled"
else
  bad "regulated HTTPS exporter did not render insecure:false"
fi

expect_fail \
  "regulated HTTPS exporter rejects insecure override" \
  "requires exporter.insecure=false" \
  --set exporter.requireEndpoint=true \
  --set exporter.endpoint=https://otlp.example.invalid:4318 \
  --set exporter.insecure=true

expect_fail \
  "regulated exporter rejects plaintext endpoint" \
  "requires an https:// exporter.endpoint" \
  --set exporter.requireEndpoint=true \
  --set exporter.endpoint=http://otlp.example.invalid:4318 \
  --set exporter.insecure=false

printf '\n--- summary: %d passed, %d failed ---\n' "${pass}" "${failures}"
exit $((failures > 0 ? 1 : 0))
