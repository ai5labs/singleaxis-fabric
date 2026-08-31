#!/usr/bin/env sh
set -eu

compose="${COMPOSE:-docker compose}"
fixture="../../tests/e2e/fixtures/decision-summary.json"

published_address() {
  service="$1"
  port="$2"
  address="$(${compose} port "${service}" "${port}" 2>/dev/null | sed -n '1p')"
  if [ -z "${address}" ]; then
    printf "service %s has no published %s/tcp port; inspect 'docker compose ps' and service logs\n" \
      "${service}" "${port}" >&2
    return 1
  fi
  case "${address}" in
    0.0.0.0:*) address="127.0.0.1:${address#*:}" ;;
    \[::\]:*) address="127.0.0.1:${address##*:}" ;;
    :::*) address="127.0.0.1:${address##*:}" ;;
  esac
  printf '%s\n' "${address}"
}

otlp_address="$(published_address fabric-node 4318)"
sink_address="$(published_address test-sink 8080)"

count() {
  response="$(curl -fsS "http://${sink_address}/count")" || return 1
  value="$(printf '%s\n' "${response}" | sed -n 's/.*"count": *\([0-9][0-9]*\).*/\1/p')"
  case "${value}" in
    ''|*[!0-9]*) return 1 ;;
    *) printf '%s\n' "${value}" ;;
  esac
}

wait_for_growth() {
  baseline="$1"
  attempts=0
  while [ "${attempts}" -lt 30 ]; do
    current="$(count 2>/dev/null || printf 0)"
    if [ "${current}" -gt "${baseline}" ]; then
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  printf 'sink count did not grow beyond %s\n' "${baseline}" >&2
  return 1
}

before="$(count)"
curl -fsS -X POST "http://${otlp_address}/v1/traces" \
  -H 'Content-Type: application/json' --data-binary "@${fixture}" >/dev/null
wait_for_growth "${before}"

if curl -fsS "http://${sink_address}/contains?needle=MUST_NOT_LEAVE_FABRIC_NODE&after=${before}" \
  | grep -q '"found": true'; then
  printf 'non-allowlisted marker crossed the Fabric Node boundary\n' >&2
  exit 1
fi
printf 'PASS: default-deny export protection removed the forbidden marker\n'

if ! curl -fsS "http://${sink_address}/contains?needle=FABRIC_E2E_RECONSTRUCTION_METADATA&after=${before}" \
  | grep -q '"found": true'; then
  printf 'allowlisted reconstruction metadata did not reach the controlled sink\n' >&2
  exit 1
fi
printf 'PASS: allowlisted reconstruction metadata reached the controlled sink\n'

before_outage="$(count)"
${compose} stop test-sink >/dev/null
curl -fsS -X POST "http://${otlp_address}/v1/traces" \
  -H 'Content-Type: application/json' --data-binary "@${fixture}" >/dev/null
# Let batch hand the request to the persistent exporter queue while the sink is
# unavailable, then restart Fabric Node to prove the queue is not memory-only.
sleep 3
${compose} restart fabric-node >/dev/null
${compose} start test-sink >/dev/null
wait_for_growth "${before_outage}"
printf 'PASS: queued request survived destination outage and Fabric Node restart\n'
printf 'NOTE: this proves at-least-once recovery to the controlled fsync sink, not exactly-once delivery or persistence semantics of arbitrary destinations.\n'
