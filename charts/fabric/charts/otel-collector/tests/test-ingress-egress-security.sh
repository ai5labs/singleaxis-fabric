#!/usr/bin/env bash
# Render contract for authenticated OTLP ingress and explicit exporter egress.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
chart_dir="$(cd "${here}/.." && pwd)"
umbrella_dir="$(cd "${chart_dir}/../.." && pwd)"
profile="${umbrella_dir}/profiles/eu-ai-act-high-risk.yaml"

passed=0
failed=0

pass() { printf 'ok: %s\n' "$*"; passed=$((passed + 1)); }
fail() { printf 'FAIL: %s\n' "$*" >&2; failed=$((failed + 1)); }

expect_contains() {
  local label="$1" haystack="$2" needle="$3"
  if grep -q -F -- "${needle}" <<<"${haystack}"; then
    pass "${label}"
  else
    fail "${label}: missing '${needle}'"
  fi
}

expect_not_contains() {
  local label="$1" haystack="$2" needle="$3"
  if grep -q -F -- "${needle}" <<<"${haystack}"; then
    fail "${label}: unexpectedly found '${needle}'"
  else
    pass "${label}"
  fi
}

expect_fail() {
  local label="$1" needle="$2"
  shift 2
  local output
  if output="$(helm template ci "$@" 2>&1)"; then
    fail "${label}: render unexpectedly succeeded"
  elif [[ "${output}" != *"${needle}"* ]]; then
    fail "${label}: expected '${needle}', got: ${output}"
  else
    pass "${label}"
  fi
}

printf '\n=== Receiver TLS and Secret projection ===\n'
default_config="$(helm template ci "${chart_dir}" --show-only templates/configmap.yaml)"
expect_not_contains "development default does not claim receiver TLS" "${default_config}" "cert_file: /etc/fabric/receiver-tls/tls.crt"

receiver_args=(
  --set receiver.requireTLS=true
  --set receiver.requireClientCertificate=true
  --set receiver.tls.serverCertificateSecret.name=fabric-test-receiver-tls
  --set receiver.tls.clientCASecret.name=fabric-test-client-ca
)
receiver_config="$(helm template ci "${chart_dir}" --show-only templates/configmap.yaml "${receiver_args[@]}")"
receiver_workload="$(helm template ci "${chart_dir}" --show-only templates/deployment.yaml "${receiver_args[@]}")"

if [[ "$(grep -c -F 'cert_file: /etc/fabric/receiver-tls/tls.crt' <<<"${receiver_config}")" -eq 2 ]]; then
  pass "gRPC and HTTP receivers both load the server certificate"
else
  fail "server certificate was not configured on both OTLP protocols"
fi
if [[ "$(grep -c -F 'client_ca_file: /etc/fabric/receiver-tls/client-ca.crt' <<<"${receiver_config}")" -eq 2 ]]; then
  pass "gRPC and HTTP receivers both verify client certificates"
else
  fail "client CA was not configured on both OTLP protocols"
fi
expect_not_contains "receiver Secret names are absent from ConfigMap" "${receiver_config}" "fabric-test-receiver-tls"
expect_not_contains "client CA Secret name is absent from ConfigMap" "${receiver_config}" "fabric-test-client-ca"
expect_contains "server certificate is projected from Secret" "${receiver_workload}" "name: fabric-test-receiver-tls"
expect_contains "client CA is projected from Secret" "${receiver_workload}" "name: fabric-test-client-ca"
expect_contains "receiver TLS projection is read-only" "${receiver_workload}" "mountPath: /etc/fabric/receiver-tls"

printf '\n=== Receiver fail-closed invariants ===\n'
expect_fail "required TLS needs a server certificate Secret" "serverCertificateSecret.name" \
  "${chart_dir}" --set receiver.requireTLS=true
expect_fail "client certificate requirement implies TLS" "requires receiver.requireTLS=true" \
  "${chart_dir}" --set receiver.requireClientCertificate=true \
  --set receiver.tls.serverCertificateSecret.name=fabric-test-receiver-tls \
  --set receiver.tls.clientCASecret.name=fabric-test-client-ca
expect_fail "required client certificates need a client CA" "clientCASecret.name" \
  "${chart_dir}" --set receiver.requireTLS=true \
  --set receiver.requireClientCertificate=true \
  --set receiver.tls.serverCertificateSecret.name=fabric-test-receiver-tls
expect_fail "client CA cannot enable TLS without server identity" "client-certificate verification cannot run without receiver TLS" \
  "${chart_dir}" --set receiver.tls.clientCASecret.name=fabric-test-client-ca

printf '\n=== Explicit exporter egress contract ===\n'
expect_fail "explicit exporter egress requires NetworkPolicy" "requires networkPolicy.enabled=true" \
  "${chart_dir}" --set exporter.endpoint=https://otlp.example.com \
  --set networkPolicy.exporterEgress.requireExplicit=true
expect_fail "explicit exporter egress requires a peer" "exporterEgress.to peer" \
  "${chart_dir}" --set exporter.endpoint=https://otlp.example.com \
  --set networkPolicy.enabled=true \
  --set networkPolicy.exporterEgress.requireExplicit=true
expect_fail "exporter peer without ports is rejected" "requires both non-empty to and ports lists" \
  "${chart_dir}" --set exporter.endpoint=https://otlp.example.com \
  --set networkPolicy.enabled=true \
  --set 'networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32'

egress_args=(
  --set exporter.endpoint=https://otlp.example.com
  --set networkPolicy.enabled=true
  --set networkPolicy.exporterEgress.requireExplicit=true
  --set 'networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32'
  --set 'networkPolicy.exporterEgress.ports[0].protocol=TCP'
  --set 'networkPolicy.exporterEgress.ports[0].port=443'
)
egress_render="$(helm template ci "${chart_dir}" "${egress_args[@]}")"
expect_contains "explicit exporter CIDR renders" "${egress_render}" "cidr: 203.0.113.10/32"
expect_contains "explicit exporter port renders" "${egress_render}" "port: 443"

printf '\n=== High-risk profile integration and locks ===\n'
high_risk_args=(
  --values "${profile}"
  --set tenant.id=11111111-1111-4111-8111-111111111111
  --set otel-collector.exporter.endpoint=https://otlp.example.com
  --set 'otel-collector.networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32'
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].protocol=TCP'
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].port=443'
  --set update-agent.config.allowPlaceholderKey=true
)

expect_fail "high-risk profile requires an operator exporter peer" "exporterEgress.to peer" \
  "${umbrella_dir}" --values "${profile}" \
  --set tenant.id=11111111-1111-4111-8111-111111111111 \
  --set otel-collector.exporter.endpoint=https://otlp.example.com \
  --set update-agent.config.allowPlaceholderKey=true

high_risk_render="$(helm template ci "${umbrella_dir}" "${high_risk_args[@]}")"
expect_contains "high-risk profile enables receiver mTLS" "${high_risk_render}" "client_ca_file: /etc/fabric/receiver-tls/client-ca.crt"
expect_contains "high-risk profile names receiver identity Secret" "${high_risk_render}" "name: fabric-otel-receiver-tls"
expect_contains "high-risk profile names client CA Secret" "${high_risk_render}" "name: fabric-otel-client-ca"
expect_contains "high-risk profile renders explicit egress peer" "${high_risk_render}" "cidr: 203.0.113.10/32"

expect_fail "high-risk receiver TLS lock cannot be disabled" "locks \"otel-collector.receiver.requireTLS\"" \
  "${umbrella_dir}" "${high_risk_args[@]}" \
  --set otel-collector.receiver.requireTLS=false \
  --set otel-collector.receiver.requireClientCertificate=false
expect_fail "high-risk client certificate lock cannot be disabled" "locks \"otel-collector.receiver.requireClientCertificate\"" \
  "${umbrella_dir}" "${high_risk_args[@]}" --set otel-collector.receiver.requireClientCertificate=false
expect_fail "high-risk explicit-egress lock cannot be disabled" "locks \"otel-collector.networkPolicy.exporterEgress.requireExplicit\"" \
  "${umbrella_dir}" "${high_risk_args[@]}" --set otel-collector.networkPolicy.exporterEgress.requireExplicit=false
expect_fail "high-risk namespace deny-default lock cannot be disabled" "locks \"networkPolicy.denyDefault\"" \
  "${umbrella_dir}" "${high_risk_args[@]}" --set networkPolicy.denyDefault=false

printf '\n--- summary: %d passed, %d failed ---\n' "${passed}" "${failed}"
exit $((failed > 0 ? 1 : 0))
