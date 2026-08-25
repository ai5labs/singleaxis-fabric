#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
production="${root}/examples/production.yaml"
development="${root}/examples/development.yaml"

grep -q 'storage: file_storage' "${production}"
grep -q 'create_directory: true' "${production}"
grep -q 'block_on_overflow: true' "${production}"
grep -q 'max_elapsed_time: 0s' "${production}"
grep -Fq '${env:FABRIC_RELAY_AUTH_HEADER}' "${production}"
test "$(grep -c 'client_ca_file:' "${production}")" -eq 2
grep -q 'exporters: \[debug\]' "${development}"

if grep -Eq '(Bearer [[:alnum:]_.-]+|api[_-]?key:[[:space:]]+[^$])' "${production}"; then
  echo "production example contains an inline credential" >&2
  exit 1
fi

if [[ -x "${root}/dist/fabric-relay" ]]; then
  FABRIC_RELAY_AUTH_HEADER=test "${root}/dist/fabric-relay" validate --config "${production}"
  "${root}/dist/fabric-relay" validate --config "${development}"
else
  echo "PASS: static relay config assertions (binary validation skipped; run 'make build validate-example')"
fi
