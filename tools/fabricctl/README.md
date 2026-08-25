# fabricctl

`fabricctl` is the canonical Go operator CLI for SingleAxis Fabric. Its current
surface helps an operator create a deterministic Offline Install Bundle v1,
inspect its desired state, and preflight a Kubernetes target without changing
a runtime:

| Command | Current behavior |
| --- | --- |
| `fabricctl init` | Guided, offline creation of the six-file Offline Install Bundle v1. |
| `fabricctl bundle build` | Non-interactive generation from reviewed `FabricDeployment` and `FabricInstallTarget` inputs. |
| `fabricctl deployment validate` | Strict local validation of a `FabricDeployment`. |
| `fabricctl deployment digest` | Validation followed by a canonical deployment digest. |
| `fabricctl deployment plan` | Deterministic, non-mutating installation/readiness planning. |
| `fabricctl doctor --offline --bundle DIR` | Strict local verification of all six bundle artifacts and their bindings. |
| `fabricctl doctor` | Read-only host, Kubernetes, Helm, profile, and optional destination preflight. |

None of these commands installs Kubernetes resources, changes cluster state,
transmits telemetry, or connects the deployment to SingleAxis services. Bundle
generation produces typed Helm values, but it is not installation, chart
rendering, verification, or proof of readiness.

## Build and run

```sh
make check
./bin/fabricctl init
./bin/fabricctl bundle build \
  --deployment ./reviewed/singleaxis.yaml \
  --target ./reviewed/install-target.yaml \
  --output-dir ./bundle \
  --json
./bin/fabricctl deployment validate singleaxis.yaml
./bin/fabricctl deployment digest singleaxis.yaml
./bin/fabricctl deployment plan singleaxis.yaml
./bin/fabricctl doctor --offline --bundle ./bundle --output json
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

## Guided Offline Install Bundle

Run the terminal wizard in the current directory:

```sh
fabricctl init
```

Use `fabricctl init --output-dir ./payments-agent` to select another directory.
The wizard must be attached to an interactive terminal: piped input is rejected
because confirmation is an operator action, not an automation interface. It is
offline and asks only for bounded configuration, public material, and opaque
non-secret references. Validation rejects common credential/token forms and
opaque token-like values; the wizard never asks for or collects secret values.

The deployment prompts are:

- Deployment name, assurance level (`A0` through `A3`), connection mode (`sdk`,
  `adapter`, `gateway`, or `otlp`), tenant identity reference, and Observe
  content mode (`metadata-only`, `hash-only`, or `content-ref`).
- A relay reference for every level except `A0`.
- Runtime control profile, assurance plan, and rollout approval references for
  `A2` and `A3`; `A3` also requires a workload identity reference.
- An optional runtime control profile for levels that do not already require
  one, followed by optional policy, authorization, runtime PII, guardrail, and
  escalation references when controls are present.

The installation-target prompts are:

- Kubernetes context, namespace, Helm release name, and whether a later
  installer may create the namespace.
- Pinned chart OCI reference, chart version, chart OCI digest, and public
  profile-file digest. The OCI reference must use `oci://`; both digests must
  be lowercase `sha256:` values. The prompts record these pins without
  downloading or verifying the referenced bytes.
- For `A2` and `A3`: registered non-secret tenant ID, approved HTTPS OTLP
  exporter endpoint, exporter egress CIDRs and TCP ports, update verification
  key ID, and a public Ed25519 update verification key.

The Ed25519 value is public verification material, not a secret. Private or
signing keys, passwords, tokens, and Kubernetes Secret values must never be
entered into the wizard.

Profile selection is deliberately constrained to profiles shipped publicly in
this release:

- `A0` selects `permissive-dev`, which is a development and synthetic-data
  posture, not a production profile.
- `A2` and `A3` select `eu-ai-act-high-risk`.
- `A1` stops with a clear error and writes no files because this release does
  not ship a production-standard profile compatible with `A1`.

Kubernetes with Helm is the only available target in Offline Install Bundle
v1. Docker Compose and local targets are unavailable rather than being treated
as production-capable installation paths.

A shortened interaction looks like this:

```text
SingleAxis Fabric desired-state initializer
This offline wizard asks only for identifiers and references, never secret values.

Deployment name (lowercase DNS-style): payments-agent-prod
Assurance level:
  1) A0 — development / synthetic-data baseline
  2) A1 — authenticated telemetry delivery
  3) A2 — controlled production deployment
  4) A3 — high-assurance regulated deployment
Select a number or name: 4
Connection mode:
  1) sdk — instrument agent code
  2) adapter — integrate a supported framework or vendor
  3) gateway — observe the LLM, MCP, or tool boundary
  4) otlp — send existing OpenTelemetry data
Select a number or name: 3
Tenant identity reference: tenant/acme-production
...
Installation target:
  Kubernetes with Helm — available for offline bundle preparation
...
Expected Kubernetes context name: production-eu
Kubernetes namespace [fabric-system]:
Helm release name [fabric]: payments-fabric
Pinned chart OCI reference [oci://ghcr.io/singleaxis/charts/fabric]:
Chart version: 1.2.3
Chart OCI digest (sha256:...): sha256:...
Profile file digest (sha256:...): sha256:...
...
Type "write" to create the six-file offline installation bundle: write
```

The review shows the complete canonical deployment, complete canonical install
target (including artifact/profile pins, egress, tenant, and public update
trust), and unresolved secret-requirements metadata before anything is
written. The exact word `write` is required to confirm. Initialization writes
exactly six local files:

| File | Class | Purpose |
| --- | --- | --- |
| `singleaxis.yaml` | canonical input | Validated `FabricDeployment` agent desired state. |
| `install-target.yaml` | canonical input | Digest-bound, pinned Kubernetes Helm target. |
| `fabric-values.yaml` | derived | Deterministic, typed Helm values generated from the canonical inputs. |
| `secrets-required.yaml` | derived | Names, keys, scopes, purpose, and consumers of required secrets—never their values. |
| `installation-plan.json` | derived | Deterministic offline actions and unresolved prerequisites. |
| `bundle-manifest.json` | derived | Byte digests for the other five files, bundle digest, and generator identity. |

For `eu-ai-act-high-risk`, all five Kubernetes Secret requirements remain
explicitly `unresolved`: receiver TLS (`fabric-otel-receiver-tls`), client CA
(`fabric-otel-client-ca`), exporter authorization
(`fabric-otel-export-auth`), sampler HMAC key
(`fabric-otel-sampler-key`), and tenant pseudonymization key
(`fabric-presidio-tenant-key`). `permissive-dev` emits no Secret requirement
entries. Generation never reads a cluster or secret store and never claims a
requirement is present.

Opaque references from `FabricDeployment`—including policy, authorization,
PII, guardrail, assurance, approval, relay, and workload identity
references—remain opaque. The generator does not resolve them or translate
them into Helm settings. `fabric-values.yaml` is limited to typed fields
derived from the install target, selected profile, and deployment identity.

All artifacts are local. `init` does not contact a network, cluster, registry,
endpoint, secret store, or platform; resolve references; download a chart;
run `helm template`; install Fabric; or apply runtime changes. Generated values
are not validated against chart bytes during bundle generation. Use `doctor`
with an operator-selected local chart, profile overlay, and generated values
file for a separate read-only Helm preflight.

The writer refuses to replace any of the six targets; there is no destructive
replacement option. Final files use no-clobber creation and restrictive
permissions, are synced before success is reported, and newly created outputs
are rolled back if the six-file write cannot complete.

The initializer resolves the existing output-directory prefix to a canonical
path, rejects any symbolic links remaining on that canonical path immediately
before commit, and rejects symbolic-link final targets. Portable filesystem
APIs cannot make inspection and parent-path resolution one indivisible
operation on every supported OS: a privileged or same-user attacker that can
rename canonical parent directories concurrently may still race path
traversal. Run `init` in an operator-controlled directory whose permissions
prevent untrusted renames. `O_CREATE|O_EXCL` still prevents an existing final
filename from being replaced.

Selecting `hash-only` reduces captured content, but hashes can remain linkable
or guessable—especially for low-entropy inputs—and are not anonymization. The
optional runtime PII reference governs the agent input/control path; it does
not configure the separate Observe/export redaction boundary.

Every successful offline plan reports `readiness: unverified`. A valid bundle
proves deterministic internal consistency only. It does not prove chart
availability, a successful Helm render, cluster access, endpoint reachability,
secret provisioning, installation, control effectiveness, compliance, or the
availability of any private SingleAxis service.

## Inspecting desired state

The [`FabricDeployment` v1alpha1 contract](../../contracts/management/v1alpha1/README.md)
is the public desired-state envelope used by the guided and automated paths:

```sh
fabricctl deployment validate singleaxis.yaml
fabricctl deployment digest singleaxis.yaml
fabricctl deployment plan singleaxis.yaml
```

Add `--json` to any deployment command for machine-readable output. Validation
is strict and fails closed with stable diagnostic IDs. Digest validates first,
then hashes canonical UTF-8 JSON containing the complete input document. Plan
validates the same resource and reports selected OSS roles, opaque references,
and unverified prerequisites without resolving or applying them.

### Automation boundary

`init` is intentionally interactive, refuses redirected stdin, and requires an
operator confirmation; it is not the CI/CD interface. Automation can inspect a
reviewed `singleaxis.yaml` with the deterministic deployment commands:

```sh
fabricctl deployment validate singleaxis.yaml --json
fabricctl deployment digest singleaxis.yaml --json
fabricctl deployment plan singleaxis.yaml --json
```

Those commands are offline and non-mutating. A digest identifies the reviewed
document; it does not prove that references exist, an approval is authorized,
or a deployment occurred.

To build the same six-file artifact set without prompts, supply both reviewed
canonical resources:

```sh
fabricctl bundle build \
  --deployment ./reviewed/singleaxis.yaml \
  --target ./reviewed/install-target.yaml \
  --output-dir ./bundle \
  --json
```

`bundle build` validates both documents and their digest binding, generates
the four derived artifacts, and writes all six files through the same
restrictive no-clobber path as `init`. Missing arguments, invalid resources,
an incompatible deployment/target pair, or an existing output filename fail
without overwriting that file. This command mutates only the requested local
output directory; it performs no network or target-environment mutation. Its
JSON envelope is `fabricctl.bundle-build/v1`. Success reports the bundle
digest, artifact paths, and `readiness: unverified`; invalid input,
cross-resource, and write failures report stable, value-free diagnostics on
stdout with the same schema.

The public [management v1alpha1 contract](../../contracts/management/v1alpha1/README.md#offline-install-bundle-v1)
defines the six artifacts and manifest algorithm. In summary, the manifest
lists the exact byte SHA-256 of the five non-manifest files and excludes
itself. To derive `bundle_digest`, sort those entries by path; for each entry,
append the UTF-8 path, one NUL byte, its lowercase 64-character SHA-256, and
one LF byte; hash the concatenation with SHA-256 and prefix the result with
`sha256:`. There is no timestamp in this deterministic identity.

Automated apply, reconciliation, upgrade, rollback, and fleet management
belong to the planned Site Controller and management-plane lifecycle; they are
not present in this CLI.

## Doctor exit behavior

`doctor` exits `1` only when a **required** check fails, `2` for invalid CLI
usage, and `0` when required checks pass. Warnings and skipped optional checks
do not fail the command.

`fabricctl doctor --offline --bundle DIR` is a separate, deterministic mode.
It accepts only `--output` in addition to those two flags and performs no host,
network, cluster, registry, endpoint, or secret-store checks. It requires the
exact six-file allowlist, verifies byte hashes and the bundle digest, rechecks
both canonical resources and their digest binding, and reconstructs every
derived artifact to reject stale content even when a modified manifest has
been re-hashed. A passing report means only internal bundle consistency; its
schema is `fabricctl.bundle-verification-report/v1` and readiness remains
`unverified`.

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

Bundle generation does not download or validate a chart. To preflight generated
high-risk values against chart bytes already obtained and reviewed by the
operator, pass the shipped profile first and the generated typed values second:

```sh
./bin/fabricctl doctor \
  --profile eu-ai-act-high-risk \
  --namespace fabric-system \
  --chart ../../charts/fabric \
  --values ../../charts/fabric/profiles/eu-ai-act-high-risk.yaml \
  --values ./bundle/fabric-values.yaml \
  --endpoint https://otlp.example.com
```

This is a read-only preflight, not evidence that the declared OCI or profile
digests were fetched or verified and not an installation or availability
attestation.

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

## Generator and version metadata

Release builds inject version, commit and build date through Go linker flags:

```sh
make build VERSION=0.1.0 COMMIT="$(git rev-parse HEAD)" DATE=2026-01-01T00:00:00Z
./bin/fabricctl version
```

Each bundle manifest records generator name, semantic version, and a full
40- or 64-character lowercase source commit. Production and evidentiary
workflows must use a released `fabricctl` whose build pipeline injected that
immutable release identity. An unversioned development build deliberately
identifies itself as `fabricctl` version `0.0.0-dev` at an all-zero commit so
local development remains deterministic. That development identity is not
release provenance and must not be represented as an approved release.

## Go and Python command ownership

The Go CLI in this directory is the canonical operator surface for guided
initialization, offline bundle generation, deployment validation, digest,
plan, and Kubernetes-aware preflight. The Python SDK currently also installs a
`fabricctl` console entry point with an older command surface. That Python CLI
is legacy during the migration to Go; do not depend on parity between the two
binaries. Packaging must ensure the intended Go executable is first on `PATH`
when using the commands documented here.

## Deliberate limitations

The current CLI generates deterministic canonical resources, typed values,
unresolved secret requirements, an offline plan, and a byte-digest manifest.
It does not fetch or verify the pinned chart/profile bytes; produce an
installation receipt; apply manifests; operate a Site Controller; reconcile
desired state; manage approvals; resolve opaque references; verify release
signatures; lock or verify every component image digest; manage upgrades or
rollback; or prove end-to-end OTLP ingestion.
It does not establish the availability of a private SingleAxis platform.

`doctor` also does not validate Secret values or ConfigMap contents, verify
policy semantics, or test NetworkPolicy dataplane behavior. The name override
flags are an explicit contract; doctor does not infer custom prerequisite
names from arbitrary YAML.

The complete interactive lifecycle—plan review, install, verify, status,
connect, upgrade, rollback, and sanitized support collection—remains planned.
Those workflows require explicit security contracts and environment-specific
evidence; the current bundle, desired-state, and preflight commands must not be
treated as deployment, operational-readiness, or compliance attestations.
