# fabric umbrella chart

The deployable unit for SingleAxis Fabric. Installs the Layer 5/6
evaluation stack into a tenant's Kubernetes cluster under one
`helm install`. Regulatory Profiles in [`profiles/`](./profiles)
preset each subchart for the target regulation.

Authoritative shape: [`specs/008-deployment-model.md`](../../specs/008-deployment-model.md).

## Phase 1 scope

- [x] Umbrella `Chart.yaml` with conditional subchart dependencies
- [x] Default `values.yaml` documenting subchart toggles
- [x] Two profiles: `permissive-dev`, `eu-ai-act-high-risk`
- [x] Cross-cutting namespace + NetworkPolicy + NOTES templates
- [x] Fabric-authored Layer 1 subcharts:
  - [`otel-collector/`](./charts/otel-collector) — telemetry processors
  - [`nemo-sidecar/`](./charts/nemo-sidecar) — NeMo Colang guardrails
  - [`presidio-sidecar/`](./charts/presidio-sidecar) — Presidio PII redaction
  - [`langfuse/`](./charts/langfuse) — local observability UI
  - [`redteam-runner/`](./charts/redteam-runner) — scheduled adversarial probes
  - [`update-agent/`](./charts/update-agent) — GitOps signed-manifest pull
- [ ] Layer 2 subcharts (`judge-workers/`, `escalation-service/`) live
      in a separate SingleAxis-internal repo during Phase 1; not part
      of the public OSS distribution.
- [ ] Decision Graph subchart (Phase 2 — awaiting Postgres migration story)
- [ ] Telemetry Bridge subchart (Phase 2)
- [ ] `values.schema.json` (Phase 2 — after subchart shape stabilizes)
- [ ] Production profiles beyond EU AI Act: NIST RMF, ISO-42001,
      SR-11-7, HIPAA (profile-by-profile as rubrics land)

## Install

```bash
cd charts/fabric
helm dependency update         # regenerates Chart.lock + charts/ tarballs
helm dependency build          # pulls subchart tarballs from charts/

# dev cluster:
helm install fabric . \
    --namespace fabric-system --create-namespace \
    --values profiles/permissive-dev.yaml

# production (EU AI Act high-risk):
helm install fabric . \
    --namespace fabric-system --create-namespace \
    --values profiles/eu-ai-act-high-risk.yaml \
    --set tenant.id=<uuid> \
    --set update-agent.config.trustedKeys[0].publicKey=<base64-ed25519-key> \
    --set otel-collector.fabric.redact.existingSocketProvider=<sidecar-name> \
    --set otel-collector.exporter.endpoint=<your-otlp-http-backend>
```

The `eu-ai-act-high-risk` profile fails closed: it will not render
without a real Ed25519 manifest-signing key, a Presidio socket
provider, and a real OTLP backend. That is deliberate — see
[Profiles and defaults](#profiles-and-defaults).

### Contributor note on `Chart.lock`

The repo intentionally does not check in `Chart.lock`. Subchart
versions are pinned in `Chart.yaml`; operators regenerate the lock
locally with `helm dependency update`. This avoids stale digests
diverging across branches when contributors bump a subchart.

If you have a `Chart.lock` left over from an older checkout,
`helm dependency build` fails with *"the lock file (Chart.lock) is
out of sync with the dependencies file (Chart.yaml)"* — delete it or
run `helm dependency update` to regenerate.

## Defaults: what a bare `helm install` gives you

`helm install fabric .` with no profile installs the **otel-collector
only**. Every other subchart stays off, because each needs something
the chart cannot invent for you (a tenant HMAC key, an Ed25519 signing
key, a Postgres DSN, a live red-team target).

| Subchart | Default | Why |
|----------|---------|-----|
| `otel-collector` | **on** | Without it a Fabric install captures nothing |
| `nemo-sidecar` | off | Needs a Colang rails bundle |
| `presidio-sidecar` | off | Needs a tenant HMAC key Secret |
| `langfuse` | off | Needs an **external** Postgres DSN — see below |
| `redteam-runner` | off | CronJob attacks a live endpoint you must name |
| `update-agent` | off | Needs a real Ed25519 manifest-signing key |

### The collector's two honest defaults

**No exporter endpoint is set.** There is no OTLP endpoint that works
in an L1-only OSS deploy, so `otel-collector.exporter.endpoint` has no
default. When it is empty the chart does **not** render an
`otlphttp/fabric` exporter pointed at `""` — it renders the OTel
`debug` exporter instead, and `NOTES.txt` prints a loud post-install
warning. Spans land in the collector pod's stdout:

```bash
kubectl -n fabric-system logs -l app.kubernetes.io/name=otel-collector -f
```

Nothing is silently dropped, but **stdout is not durable and is not an
audit trail**. It is a dev posture. Before any retention or compliance
claim, point the collector at a real backend:

```bash
helm upgrade fabric . -n fabric-system \
    --set otel-collector.exporter.endpoint=http://your-collector:4318
```

**The HMAC sampler is off.** `otel-collector.fabric.sampler` needs a
32-byte key the chart will not generate for you — an auto-generated key
would rotate on every `helm upgrade` and silently change every
deterministic sampling decision. Off is also the conservative choice
for an audit trail: the sampler *reduces* data (its default
`decision_summary` rate drops 90% of decision summaries), so a bare
install keeps everything. Turn it on with a key once volume is the
problem:

```bash
--set otel-collector.fabric.sampler.enabled=true \
--set otel-collector.fabric.sampler.hmacKeySecret.name=fabric-otel-sampler-key
```

## Profiles and defaults

Both shipped profiles set every toggle they care about explicitly, so
neither is affected by the umbrella defaults above.

- **`permissive-dev`** — otel-collector + nemo-sidecar, single replica,
  redaction off, sampler on with a *known-constant* dev key, and the
  debug exporter on. No exporter endpoint: this profile has no backend
  to export to, so spans go to pod stdout by design. Not for
  production.
- **`eu-ai-act-high-risk`** — deny-default NetworkPolicy, guard with
  `dropUnknownClasses`, trace processing and redaction pinned on, sampler keyed from a
  Secret, update-agent admission fail-closed. It renders **only** when
  you supply the real secrets it refuses to fake:
  1. `update-agent.config.trustedKeys[0].publicKey` — a real base64
     Ed25519 key.
  2. `otel-collector.fabric.redact.existingSocketProvider` — the
     component mounting the Presidio socket (or enable the bundled
     `presidioSidecar` and point at it).

  3. `otel-collector.exporter.endpoint` — this profile sets
     `exporter.requireEndpoint: true`, which disables the stdout
     fallback. A profile that makes a retention claim must name a real
     backend rather than inherit a dev posture.

  For a dry render only, all three gates have escape hatches:
  `--set update-agent.config.allowPlaceholderKey=true`,
  `--set otel-collector.fabric.redact.acceptMissingProvider=true`, and
  `--set otel-collector.exporter.requireEndpoint=false`. Never set any
  of them in a real install.

### Langfuse is opt-in and does not bundle Postgres

The `langfuse` subchart is a thin wrapper around the upstream Langfuse
image. **It ships no database.** `langfuse.enabled=true` without
`langfuse.database.url` or `langfuse.database.dsnSecret.name` fails at
render time rather than deploying a pod that cannot start. Two further
caveats before you wire the collector to it:

- The Service is named `<release>-langfuse` (e.g. `fabric-langfuse`),
  not `langfuse`.
- Whether the pinned upstream version accepts OTLP/HTTP on
  `/v1/traces` at all is **unverified**. Do not assume the bundled
  Langfuse is a drop-in OTLP sink for `exporter.endpoint`.

## Latency posture (cross-cutting)

Every component is gated on a per-operation latency budget. The
agent's request path is *never* synchronous on a Fabric HTTP call:

| Layer | Operation | Budget (P99) |
|-------|-----------|--------------|
| SDK | span emit + local decision update | <1ms |
| L5 guardrails | UDS sidecar check | <100ms |
| L6 judges (fast) | score async | <500ms |
| L7 escalation | publish to bus | <5ms |
| L7 escalation | SDK resume poll | <5ms |

The numbers above are design budgets per spec 005, not measured P99s
on the current release. Today's readiness probes are simple HTTP
`/healthz` checks (process up). A latency-aware readiness gate that
flips `NotReady` on budget breach is roadmap; the benchmark suite
that would inform it lands as a follow-up release. Documented in
each component README.

## Chart structure

`fabric/` is the **only published unit** — the release pipeline packages
and pushes this umbrella chart as a single OCI artifact. The subcharts
below are bundled inside it; they are not released or installed on their
own. First-party (Fabric-authored) subcharts have their `appVersion`
bumped to the Fabric release version on each release; `langfuse` is a
third-party dependency pinned independently to its upstream version.

```
charts/fabric/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── _helpers.tpl
│   ├── namespace.yaml
│   ├── networkpolicy.yaml
│   └── NOTES.txt
├── charts/
│   ├── otel-collector/      (Layer 1, first-party)
│   ├── nemo-sidecar/        (Layer 1, first-party)
│   ├── presidio-sidecar/    (Layer 1, first-party)
│   ├── langfuse/            (Layer 1, third-party — pinned to upstream Langfuse)
│   ├── redteam-runner/      (Layer 1, first-party)
│   └── update-agent/        (Layer 1, first-party)
└── profiles/
    ├── permissive-dev.yaml
    └── eu-ai-act-high-risk.yaml
```

### Langfuse versioning

The `langfuse` subchart's `appVersion` tracks the **upstream Langfuse**
release and is intentionally NOT bumped with Fabric releases. The one
piece that does carry the Fabric version is the Fabric-built
`langfuse-bootstrap` image (curated-bundle seeding tool): the umbrella
propagates `global.fabric.version` so the bootstrap Job tags that image
at the Fabric release version the pipeline actually publishes it at,
while the Langfuse application container keeps its upstream tag.

## Release signing

The `fabric` umbrella chart is published as a signed OCI artifact —
signed keylessly with [cosign](https://www.sigstore.dev/) via Fulcio
(see the `publish-chart` job in `.github/workflows/release.yml`).
Verification instructions ship with each release — see
[`SECURITY.md`](../../SECURITY.md) §Release signing.

## Testing

```bash
helm lint charts/fabric
helm template test charts/fabric > /dev/null
helm template test charts/fabric --values charts/fabric/profiles/permissive-dev.yaml > /dev/null

# eu-ai-act-high-risk fails closed on two secrets it refuses to fake.
# These two flags affect rendering only — never use them to install.
helm template test charts/fabric \
    --values charts/fabric/profiles/eu-ai-act-high-risk.yaml \
    --set update-agent.config.allowPlaceholderKey=true \
    --set otel-collector.fabric.redact.acceptMissingProvider=true \
    --set otel-collector.exporter.requireEndpoint=false > /dev/null

# Subchart render assertions:
./charts/fabric/charts/otel-collector/tests/test-hmackey-validation.sh
./charts/fabric/charts/otel-collector/tests/test-traces-pipeline.sh
```

Note that `helm lint` does **not** propagate a subchart's `fail` into
its exit code — it logs `[INFO] Fail: ...` and still reports
`0 chart(s) failed`. Always run `helm template` as well; it is the
command that actually exits non-zero on a fail-closed gate.
