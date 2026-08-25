#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
UMBRELLA="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
COLLECTOR="${UMBRELLA}/charts/otel-collector"
NEMO="${UMBRELLA}/charts/nemo-sidecar"
INVALID="${SCRIPT_DIR}/invalid-values"

pass_count=0

pass() {
  pass_count=$((pass_count + 1))
  printf 'ok %02d - %s\n' "${pass_count}" "$1"
}

render_ok() {
  local description="$1"
  shift
  helm template fabric-schema-test "$@" >/dev/null
  pass "${description}"
}

lint_ok() {
  local description="$1"
  shift
  helm lint "$@" >/dev/null
  pass "${description}"
}

reject_values() {
  local description="$1"
  local chart="$2"
  local fixture="$3"
  local expected="$4"
  local output

  if output="$(helm template fabric-schema-invalid "${chart}" --values "${fixture}" 2>&1)"; then
    printf 'not ok - %s: Helm unexpectedly accepted %s\n' "${description}" "${fixture}" >&2
    exit 1
  fi
  if ! grep -Fq -- "${expected}" <<<"${output}"; then
    printf 'not ok - %s: failure did not contain %q\n%s\n' \
      "${description}" "${expected}" "${output}" >&2
    exit 1
  fi
  pass "${description}"
}

reject_high_risk_override() {
  local description="$1"
  local path="$2"
  local value="$3"
  local output

  if output="$(helm template fabric-high-risk-adversary "${UMBRELLA}" \
    --values "${UMBRELLA}/profiles/eu-ai-act-high-risk.yaml" \
    --set tenant.id=00000000-0000-4000-8000-000000000001 \
    --set otel-collector.exporter.endpoint=https://otlp.example.invalid \
    --set 'otel-collector.networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32' \
    --set 'otel-collector.networkPolicy.exporterEgress.ports[0].protocol=TCP' \
    --set 'otel-collector.networkPolicy.exporterEgress.ports[0].port=443' \
    --set 'update-agent.config.trustedKeys[0].publicKey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=' \
    --set-json 'profile.lockedFields=[]' \
    --set "${path}=${value}" 2>&1)"; then
    printf 'not ok - %s: high-risk render accepted %s=%s with an empty lock list\n' \
      "${description}" "${path}" "${value}" >&2
    exit 1
  fi
  # A subchart may reject the weakened combination before the parent chart's
  # immutable high-risk table runs. Either rejection is valid: the supported
  # baseline rendered above, and this exact weakening did not.
  pass "${description}"
}

# Safe, supported configurations.
lint_ok "umbrella defaults lint" "${UMBRELLA}"
lint_ok "Collector defaults lint" "${COLLECTOR}"
lint_ok "NeMo defaults lint" "${NEMO}"
render_ok "umbrella defaults render" "${UMBRELLA}"
render_ok "permissive-dev profile renders" \
  "${UMBRELLA}" --values "${UMBRELLA}/profiles/permissive-dev.yaml"
render_ok "EU high-risk profile renders with deployment-owned references" \
  "${UMBRELLA}" \
  --values "${UMBRELLA}/profiles/eu-ai-act-high-risk.yaml" \
  --set tenant.id=00000000-0000-4000-8000-000000000001 \
  --set otel-collector.exporter.endpoint=https://otlp.example.invalid \
  --set 'otel-collector.networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32' \
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].protocol=TCP' \
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].port=443' \
  --set 'update-agent.config.trustedKeys[0].publicKey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA='
render_ok "Collector embedded redaction provider renders" \
  "${COLLECTOR}" \
  --set fabric.redact.enabled=true \
  --set fabric.redact.embedded.enabled=true \
  --set fabric.redact.embedded.tenantKeySecret.name=fabric-redaction-key
render_ok "Collector policy bundle reference renders" \
  "${COLLECTOR}" \
  --set fabric.policy.enabled=true \
  --set fabric.policy.bundlePath=/etc/fabric/policy \
  --set fabric.policy.bundleConfigMap=fabric-approved-policy
render_ok "NeMo custom rails mode renders" \
  "${NEMO}" \
  --set railsConfigMap.name=fabric-approved-rails \
  --set starterRails.enabled=false
render_ok "NeMo explicit development passthrough renders" \
  "${NEMO}" \
  --set starterRails.enabled=false \
  --set allowPassthrough=true

# Closed contracts and cross-field safety invariants.
reject_values "umbrella rejects misspelled component" \
  "${UMBRELLA}" "${INVALID}/umbrella-unknown-key.yaml" "additional properties 'otelColletor' not allowed"
reject_values "umbrella rejects string component toggle" \
  "${UMBRELLA}" "${INVALID}/umbrella-toggle-type.yaml" "got string, want boolean"
reject_values "Collector rejects misspelled guard field" \
  "${COLLECTOR}" "${INVALID}/collector-unknown-key.yaml" "additional properties 'dropUnkownClasses' not allowed"
reject_values "Collector sampler requires exactly one credential source" \
  "${COLLECTOR}" "${INVALID}/collector-sampler-credential-missing.yaml" "requires fabric.sampler.hmacKey"
reject_values "Collector rejects unsafe exporter verbosity" \
  "${COLLECTOR}" "${INVALID}/collector-unsafe-enum.yaml" "value must be one of 'basic', 'normal', 'detailed'"
reject_values "NeMo rejects misspelled values" \
  "${NEMO}" "${INVALID}/nemo-unknown-key.yaml" "additional properties 'starterRail' not allowed"
reject_values "NeMo starter and passthrough modes are exclusive" \
  "${NEMO}" "${INVALID}/nemo-starter-passthrough.yaml" "value must be false"
reject_values "NeMo starter cannot disable its deterministic filter" \
  "${NEMO}" "${INVALID}/nemo-starter-filter-disabled.yaml" "value must be true"
reject_values "NeMo requires rails or explicit passthrough" \
  "${NEMO}" "${INVALID}/nemo-no-mode.yaml" "value must be true"
reject_values "NeMo rejects unsupported image pull policy" \
  "${NEMO}" "${INVALID}/nemo-unsafe-enum.yaml" "value must be one of 'Always', 'IfNotPresent', 'Never'"

# The high-risk contract is keyed on profile identity. These attacks clear the
# user-mutable documentation list before weakening a control; every render must
# still fail against the chart-owned invariant table.
reject_high_risk_override "high-risk cannot disable Collector" \
  "otelCollector.enabled" "false"
reject_high_risk_override "high-risk cannot disable guard processing" \
  "otel-collector.fabric.guard.enabled" "false"
reject_high_risk_override "high-risk cannot retain unknown trace classes" \
  "otel-collector.fabric.guard.dropUnknownClasses" "false"
reject_high_risk_override "high-risk cannot disable trace processing" \
  "otel-collector.fabric.guard.traceProcessingEnabled" "false"
reject_high_risk_override "high-risk cannot disable PII redaction" \
  "otel-collector.fabric.redact.enabled" "false"
reject_high_risk_override "high-risk cannot disable egress policy" \
  "otel-collector.fabric.policy.enabled" "false"
reject_high_risk_override "high-risk cannot remove required destination" \
  "otel-collector.exporter.requireEndpoint" "false"
reject_high_risk_override "high-risk cannot disable update agent" \
  "updateAgent.enabled" "false"
reject_high_risk_override "high-risk cannot fail open in update verifier" \
  "update-agent.config.failClosed" "false"
reject_high_risk_override "high-risk cannot ignore webhook failure" \
  "update-agent.webhook.failurePolicy" "Ignore"

printf 'PASS: %d Helm schema checks\n' "${pass_count}"
