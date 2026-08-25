#!/usr/bin/env bash
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

expect_failure() {
  local expected="$1"
  shift
  local output
  if output="$(helm template relay "${chart_dir}" "$@" 2>&1)"; then
    fail "render unexpectedly succeeded: $*"
  fi
  grep -q "${expected}" <<<"${output}" || {
    echo "${output}" >&2
    fail "failure did not contain: ${expected}"
  }
}

expect_failure_either() {
  local expected_a="$1"
  local expected_b="$2"
  shift 2
  local output
  if output="$(helm template relay "${chart_dir}" "$@" 2>&1)"; then
    fail "render unexpectedly succeeded: $*"
  fi
  if ! grep -Fq "${expected_a}" <<<"${output}" && ! grep -Fq "${expected_b}" <<<"${output}"; then
    echo "${output}" >&2
    fail "failure contained neither supported Helm error: ${expected_a} | ${expected_b}"
  fi
}

default_render="$(helm template relay "${chart_dir}")"
grep -q 'kind: StatefulSet' <<<"${default_render}" || fail "default is not a StatefulSet"
grep -q 'emptyDir: {}' <<<"${default_render}" || fail "development queue is not visibly ephemeral"
grep -q 'exporters: \[debug\]' <<<"${default_render}" || fail "development fallback is not debug"

deny_render="$(helm template relay "${chart_dir}" --set networkPolicy.enabled=true)"
grep -q 'ingress: \[\]' <<<"${deny_render}" || fail "empty ingressFrom is not deny-all"
if grep -q 'namespaceSelector: {}' <<<"${deny_render}"; then
  fail "empty ingressFrom rendered cluster-wide access"
fi

production_args=(
  -f "${chart_dir}/tests/production-values.yaml"
)

production_render="$(helm template relay "${chart_dir}" "${production_args[@]}")"
grep -q 'kind: StatefulSet' <<<"${production_render}" || fail "production is not a StatefulSet"
grep -q 'volumeClaimTemplates:' <<<"${production_render}" || fail "production has no PVC template"
grep -q 'storageClassName: "encrypted-rwo"' <<<"${production_render}" || fail "storage class missing"
grep -q 'storage: file_storage' <<<"${production_render}" || fail "persistent sending queue missing"
grep -q 'create_directory: true' <<<"${production_render}" || fail "queue directory creation missing"
grep -q 'block_on_overflow: true' <<<"${production_render}" || fail "queue backpressure missing"
grep -q 'max_elapsed_time: 0s' <<<"${production_render}" || fail "indefinite retry missing"
grep -q 'valueFrom:' <<<"${production_render}" || fail "auth is not sourced from a Secret"
grep -Fq "\${env:FABRIC_RELAY_AUTH_HEADER}" <<<"${production_render}" || fail "config has no env reference"
test "$(grep -c 'client_ca_file:' <<<"${production_render}")" -eq 2 || fail "receiver mTLS is not configured for both protocols"
grep -q 'secretName: relay-server-tls' <<<"${production_render}" || fail "receiver server TLS Secret not mounted"
grep -q 'secretName: relay-client-ca' <<<"${production_render}" || fail "receiver client CA Secret not mounted"
grep -q 'access: fabric-relay' <<<"${production_render}" || fail "explicit ingress selector not rendered"
grep -q 'cidr: 203.0.113.0/24' <<<"${production_render}" || fail "explicit egress CIDR not rendered"

# There is no inline credential input in the schema. This must be rejected,
# preventing credentials from entering ConfigMaps, Helm release state or GitOps.
expect_failure_either "additional properties 'value' not allowed" \
  "Additional property value is not allowed" \
  --set destination.auth.value=Bearer-secret

expect_failure_either "additional properties 'certificate' not allowed" \
  "Additional property certificate is not allowed" \
  --set receiver.tls.serverCertificateSecret.certificate=private-material

expect_failure 'does not match pattern' \
  "${production_args[@]}" \
  --set destination.endpoint=https://user:password@offline.example.invalid \
  --set 'destination.allowedEndpoints[0]=https://user:password@offline.example.invalid'

expect_failure 'does not match pattern' \
  "${production_args[@]}" \
  --set 'destination.endpoint=https://offline.example.invalid?token=secret' \
  --set 'destination.allowedEndpoints[0]=https://offline.example.invalid?token=secret'

expect_failure 'does not match pattern' \
  "${production_args[@]}" \
  --set 'destination.endpoint=https://offline.example.invalid#secret' \
  --set 'destination.allowedEndpoints[0]=https://offline.example.invalid#secret'

expect_failure 'value must be false' \
  "${production_args[@]}" --set destination.tls.insecureSkipVerify=true

expect_failure 'production destination.endpoint must exactly match' \
  --set mode=production \
  --set destination.endpoint=https://unapproved.example.invalid \
  --set 'destination.allowedEndpoints[0]=https://approved.example.invalid' \
  --set persistence.enabled=true \
  --set debugExporter.enabled=false \
  --set receiver.tls.serverCertificateSecret.name=relay-server-tls \
  --set receiver.tls.clientCASecret.name=relay-client-ca \
  --set networkPolicy.enabled=true \
  --set networkPolicy.ingressFrom[0].namespaceSelector.matchLabels.access=fabric-relay \
  --set networkPolicy.egressTo[0].ipBlock.cidr=203.0.113.0/24

expect_failure 'value must be true' \
  "${production_args[@]}" --set networkPolicy.enabled=false

expect_failure 'minProperties: got 0, want 1' \
  "${production_args[@]}" --set-json 'networkPolicy.ingressFrom=[{}]'

expect_failure 'minProperties: got 0, want 1' \
  "${production_args[@]}" --set-json 'networkPolicy.ingressFrom=[{"namespaceSelector":{}}]'

expect_failure 'minProperties: got 0, want 1' \
  "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{}]'

expect_failure 'minProperties: got 0, want 1' \
  "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"namespaceSelector":{}}]'

expect_failure "'oneOf' failed" \
  "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"ipBlock":{"cidr":"0.0.0.0/0"}}]'

expect_failure "'oneOf' failed" \
  "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"ipBlock":{"cidr":"1.2.3.4/0"}}]'

expect_failure "'oneOf' failed" \
  "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"ipBlock":{"cidr":"2001:db8::1/0"}}]'

expect_failure "'oneOf' failed" \
  "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"ipBlock":{"cidr":"999.1.1.1/24"}}]'

expect_failure 'minItems: got 0, want 1' \
  "${production_args[@]}" --set-json 'networkPolicy.egressTo=[]'

expect_failure 'does not match pattern' --set queue.directory=/dev/shm/fabric-relay

expect_failure 'does not match pattern' --set queue.directory=/var/lib/fabric-relay/queue/../escape

# Exercise the Helm validation as defense in depth, independently of schema.
expect_failure 'must not be an unrestricted empty peer' \
  --skip-schema-validation "${production_args[@]}" --set-json 'networkPolicy.ingressFrom=[{}]'

expect_failure 'IPv4 CIDR prefix length must be between 1 and 32' \
  --skip-schema-validation "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"ipBlock":{"cidr":"0.0.0.0/0"}}]'

expect_failure 'IPv4 CIDR prefix length must be between 1 and 32' \
  --skip-schema-validation "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"ipBlock":{"cidr":"1.2.3.4/0"}}]'

expect_failure 'IPv6 CIDR prefix length must be between 1 and 128' \
  --skip-schema-validation "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"ipBlock":{"cidr":"2001:db8::1/0"}}]'

expect_failure 'contains an out-of-range octet' \
  --skip-schema-validation "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"ipBlock":{"cidr":"999.1.1.1/24"}}]'

expect_failure 'has invalid compression' \
  --skip-schema-validation "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"ipBlock":{"cidr":"2001:::1/64"}}]'

expect_failure 'is not a canonical network address' \
  --skip-schema-validation "${production_args[@]}" --set-json 'networkPolicy.egressTo=[{"ipBlock":{"cidr":"203.0.113.5/24"}}]'

expect_failure 'must resolve beneath the PVC mount' \
  --skip-schema-validation --set queue.directory=/dev/shm/fabric-relay

expect_failure 'must resolve beneath the PVC mount' \
  --skip-schema-validation --set queue.directory=/var/lib/fabric-relay/queue/../escape

expect_failure '/receiver/tls/clientCASecret/name' \
  --set mode=production \
  --set destination.endpoint=https://offline.example.invalid \
  --set 'destination.allowedEndpoints[0]=https://offline.example.invalid' \
  --set persistence.enabled=true \
  --set debugExporter.enabled=false \
  --set networkPolicy.enabled=true \
  --set networkPolicy.ingressFrom[0].namespaceSelector.matchLabels.access=fabric-relay \
  --set networkPolicy.egressTo[0].ipBlock.cidr=203.0.113.0/24

expect_failure 'minItems: got 0, want 1' \
  --set mode=production \
  --set destination.endpoint=https://offline.example.invalid \
  --set 'destination.allowedEndpoints[0]=https://offline.example.invalid' \
  --set persistence.enabled=true \
  --set debugExporter.enabled=false \
  --set receiver.tls.serverCertificateSecret.name=relay-server-tls \
  --set receiver.tls.clientCASecret.name=relay-client-ca \
  --set networkPolicy.enabled=true

echo "PASS: Fabric Relay development, production persistence, queue, Secret and rejection assertions"
