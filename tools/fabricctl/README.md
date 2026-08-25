# fabricctl

`fabricctl` is the local, read-only preflight CLI for SingleAxis Fabric. The
first release slice provides `fabricctl doctor`; it does not install resources,
change cluster state, transmit telemetry, or connect to SingleAxis services.

## Build and run

```sh
make check
./bin/fabricctl doctor
./bin/fabricctl doctor --profile permissive-dev --output json
./bin/fabricctl doctor \
  --profile eu-ai-act-high-risk \
  --namespace fabric-system \
  --chart ../../charts/fabric \
  --values ../../charts/fabric/profiles/eu-ai-act-high-risk.yaml \
  --values ./acme-production.yaml \
  --endpoint https://otlp.example.com
```

The command exits `1` only when a **required** check fails, `2` for invalid CLI
usage, and `0` when required checks pass. Warnings and skipped optional checks
do not fail the command.

## Checks and security posture

Doctor reports stable result codes using the `fabricctl.doctor.v1` JSON
contract. Each result includes `severity`, `status`, `required`, `summary`,
`remediation`, and `evidence`.

- Host OS and architecture support.
- `kubectl` and `helm` presence and client versions. Helm is mandatory for the
  high-risk profile.
- Current Kubernetes context, API readiness, and non-mutating `kubectl auth
  can-i` signals for the selected namespace.
- A read-only `helm template` of the exact local chart and ordered values
  overlays. This is mandatory for `eu-ai-act-high-risk`, includes Kubernetes
  OpenAPI validation against the selected cluster, and catches chart failures
  such as an unset tenant, destination, or trusted update signing key. Doctor
  suppresses all Helm stdout and stderr because rendered manifests can contain
  Secret data.
- For high-risk, a successful render and profile label are not sufficient.
  Doctor also requires the chart-owned assurance-contract marker and effective
  rendered evidence for guard, policy, PII redaction, trace processing, secure
  export, fail-closed update-agent configuration, deployment, and webhook
  `failurePolicy: Fail`. Missing proof fails the required Helm check without
  copying rendered data into the report.
- Built-in profile requirements. `eu-ai-act-high-risk` requires an HTTPS
  destination plus the named Presidio and sampler Secrets, policy ConfigMap,
  and approved rails ConfigMap defined by the chart profile.
- Optional destination validation and a timeout-bounded HTTP `HEAD` probe.
- An explicit warning that manifest inspection cannot prove a CNI enforces
  NetworkPolicy.

For Kubernetes prerequisite checks, doctor runs `kubectl get` with
`--output=name`; that GET output is limited to the object name and Secret
values are never printed. Helm still has to read the operator-selected local
values files and may render Secret resources in process memory to validate the
real deployment, so its complete output is discarded and never included in
the report. Endpoint URLs containing user information, query strings, or
fragments are rejected to keep credentials out of reports. HTTP redirects are
not followed. The command does not phone home except for the explicit,
opt-in `HEAD` probe to `--endpoint`.

`--chart` and every `--values` input must be an existing local path. URLs and
standard input are rejected. The CLI deliberately has no `--set`, `--set-file`,
or `--set-string` passthrough: put deployment inputs in reviewed values files
so the preflight and installation use the same auditable overlays.

The compiled prerequisite names follow the shipped high-risk profile. If an
approved customer overlay changes those names, declare the same non-secret
names explicitly:

```sh
./bin/fabricctl doctor \
  --profile eu-ai-act-high-risk \
  --chart ../../charts/fabric \
  --values ../../charts/fabric/profiles/eu-ai-act-high-risk.yaml \
  --values ./acme-production.yaml \
  --endpoint https://otlp.example.com \
  --policy-configmap acme-egress-v7 \
  --rails-configmap acme-rails-v12 \
  --presidio-key-secret acme-presidio-key \
  --sampler-key-secret acme-sampler-key
```

JSON is intended for CI and evidence collection:

```sh
./bin/fabricctl doctor --output json > doctor.json
```

Review report evidence before sharing it: context, namespace, binary paths and
destination hostnames may still be sensitive operational metadata.

## Version metadata

Release builds inject version, commit and build date through Go linker flags:

```sh
make build VERSION=0.1.0 COMMIT="$(git rev-parse --short HEAD)" DATE=2026-01-01T00:00:00Z
./bin/fabricctl version
```

## Deliberate limitations

This slice does not apply manifests, prove end-to-end OTLP ingestion, validate
the contents of existing Secret or ConfigMap objects, verify policy semantics,
or test NetworkPolicy dataplane behavior. The name override flags are an
explicit contract; doctor does not infer custom prerequisite names from
arbitrary YAML. Those deeper checks require explicit install/verify workflows
and environment-specific evidence rather than generic claims.
