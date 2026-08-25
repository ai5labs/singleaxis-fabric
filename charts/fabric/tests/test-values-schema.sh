#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
chart_dir="$(cd "${script_dir}/.." && pwd)"
collector_dir="${chart_dir}/charts/otel-collector"
fixtures_dir="${script_dir}/fixtures"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

expect_schema_failure() {
  local description="$1"
  local expected="$2"
  shift 2

  local output
  if output="$(helm template schema-test "$@" 2>&1)"; then
    fail "${description}: Helm accepted invalid values"
  fi
  if [[ "${output}" != *"values don't meet the specifications of the schema"* ]]; then
    printf '%s\n' "${output}" >&2
    fail "${description}: failure did not come from values schema validation"
  fi
  if [[ "${output}" != *"${expected}"* ]]; then
    printf '%s\n' "${output}" >&2
    fail "${description}: expected error fragment '${expected}'"
  fi
}

expect_render_failure() {
  local description="$1"
  local expected="$2"
  shift 2

  local output
  if output="$(helm template render-test "$@" 2>&1)"; then
    fail "${description}: Helm accepted an invalid high-risk posture"
  fi
  if [[ "${output}" != *"${expected}"* ]]; then
    printf '%s\n' "${output}" >&2
    fail "${description}: expected error fragment '${expected}'"
  fi
}

high_risk_args=(
  "${chart_dir}"
  --values "${chart_dir}/profiles/eu-ai-act-high-risk.yaml"
  --set tenant.id=11111111-1111-4111-8111-111111111111
  --set otel-collector.exporter.endpoint=https://otlp.example.com
  --set 'otel-collector.networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32'
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].protocol=TCP'
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].port=443'
  --set update-agent.config.allowPlaceholderKey=true
)

# Supported installation paths must all pass schema validation and rendering.
helm lint "${chart_dir}" >/dev/null
helm lint "${collector_dir}" >/dev/null
helm template schema-default "${chart_dir}" >/dev/null
helm template schema-dev "${chart_dir}" \
  --values "${chart_dir}/profiles/permissive-dev.yaml" >/dev/null
helm template schema-n-minus-one "${chart_dir}" \
  --values "${fixtures_dir}/v0.6-supported-values.yaml" >/dev/null
helm template schema-high-risk "${high_risk_args[@]}" >/dev/null

# Regulated deployments fail loudly if an override weakens admission or puts
# a generated webhook private key into Helm release state.
expect_render_failure \
  "high-risk self-signed webhook TLS" "requires update-agent.tls.mode=certManager" \
  "${high_risk_args[@]}" --set update-agent.tls.mode=selfSigned
expect_render_failure \
  "high-risk disabled admission" "requires updateAgent.enabled=true" \
  "${high_risk_args[@]}" --set updateAgent.enabled=false
expect_render_failure \
  "high-risk fail-open admission" "requires update-agent.config.failClosed=true" \
  "${high_risk_args[@]}" --set update-agent.config.failClosed=false
expect_render_failure \
  "high-risk empty certificate issuer" "requires a non-empty update-agent.tls.certManager.issuerRef.name" \
  "${high_risk_args[@]}" --set update-agent.tls.certManager.issuerRef.name=

# Root and security-critical stable objects reject misspelled or unknown keys.
expect_schema_failure \
  "unknown umbrella component" "telemetryCollector" \
  "${chart_dir}" --set telemetryCollector.enabled=true
expect_schema_failure \
  "misspelled TLS requirement" "requireTlS" \
  "${chart_dir}" --set otel-collector.exporter.requireTlS=true
expect_schema_failure \
  "misspelled receiver client-certificate requirement" "requireClientCertficate" \
  "${chart_dir}" --set otel-collector.receiver.requireClientCertficate=true
expect_schema_failure \
  "wrong queue-size type" "queueSize" \
  "${collector_dir}" --set exporter.sendingQueue.queueSize=unbounded
expect_schema_failure \
  "out-of-range sampling rate" "decision_summary" \
  "${collector_dir}" --set-json fabric.sampler.rates.decision_summary=1.1
expect_schema_failure \
  "invalid redaction byte handling" "byteHandling" \
  "${collector_dir}" --set fabric.redact.byteHandling=base64
expect_schema_failure \
  "invalid OTLP port" "otlpHttp" \
  "${collector_dir}" --set service.ports.otlpHttp=0
expect_schema_failure \
  "invalid exporter egress protocol" "protocol" \
  "${collector_dir}" \
  --set 'networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32' \
  --set 'networkPolicy.exporterEgress.ports[0].protocol=HTTPS' \
  --set 'networkPolicy.exporterEgress.ports[0].port=443'

# v0.6 cross-pod UDS declarations were unsafe and are removed, not silently
# ignored. The migration is documented in upgrading-v0.6-to-v0.7.md.
expect_schema_failure \
  "removed existingSocketProvider" "existingSocketProvider" \
  "${collector_dir}" --set fabric.redact.existingSocketProvider=legacy-provider
expect_schema_failure \
  "removed acceptMissingProvider" "acceptMissingProvider" \
  "${collector_dir}" --set fabric.redact.acceptMissingProvider=true

printf 'PASS: umbrella and collector values schemas\n'
