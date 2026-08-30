#!/usr/bin/env sh
set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
manifest="${root}/ocb-config.yaml"
dockerfile="${root}/Dockerfile"

grep -q 'processor/fabricguardprocessor' "${manifest}"
grep -q 'COPY processor/fabricguardprocessor' "${dockerfile}"

for prohibited in fabricpolicyprocessor fabricredactprocessor fabricsamplerprocessor; do
  if grep -q "${prohibited}" "${manifest}"; then
    echo "recorder manifest includes prohibited processor: ${prohibited}" >&2
    exit 1
  fi
done

if grep -Eq '^COPY processor([[:space:]]|$)' "${dockerfile}"; then
  echo 'recorder image copies every legacy processor source' >&2
  exit 1
fi

for security_floor in \
  'golang.org/x/crypto => golang.org/x/crypto v0.53.0' \
  'golang.org/x/net => golang.org/x/net v0.56.0' \
  'golang.org/x/text => golang.org/x/text v0.39.0' \
  'google.golang.org/grpc => google.golang.org/grpc v1.82.1'; do
  if ! grep -Fq -- "${security_floor}" "${manifest}"; then
    echo "recorder manifest is missing security floor: ${security_floor}" >&2
    exit 1
  fi
done

echo 'PASS: recorder build manifest contains only fabricguard'
