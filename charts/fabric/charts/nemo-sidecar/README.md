# nemo-sidecar subchart

NeMo Colang guardrails sidecar. Spec:
[`specs/005-guardrails-inline.md`](../../../../specs/005-guardrails-inline.md).

## What this ships

A FastAPI sidecar that evaluates prompts and outputs against a Colang
rails configuration and an optional deterministic literal pre-filter:

- `GET  /healthz`
- `POST /v1/check` — returns `{allowed, action, rail, block_response, modified_value}`

## Phase 1 scope

- Shared `Deployment` + `Service` mode over TCP for smoke tests and
  development clusters. This chart does not provide sidecar injection
  or assert a production latency SLO.
- A model-free starter bundle is enabled by default. Helm selects the
  real `--starter-literal-only` engine, which validates the declaration,
  blocks known instruction-override phrases, and allows benign input
  without initializing NeMo or making an external call.
- Passthrough is refused unless the operator disables the starter and
  explicitly sets `allowPassthrough=true` for development.

## Canonical starter bundle

The canonical starter source is
`components/nemo-sidecar/rails/starter`. Helm packages generated,
byte-identical mirrors from `files/rails/starter` and reads them with
`.Files.Get`; the ConfigMap does not maintain a second handwritten copy.

After changing the canonical files, refresh the chart package from the
repository root:

```sh
python components/nemo-sidecar/tools/sync_starter_chart_bundle.py
python components/nemo-sidecar/tools/sync_starter_chart_bundle.py --check
```

Tests compare both packaged bytes and parsed Helm-rendered ConfigMap
values to the canonical source. Extra stale flows therefore fail the
same check as missing flows.

## Auth: shared token on `/v1/*`

By default the TCP listener is unauthenticated. Set `auth.tokenSecret`
to have every `/v1/*` request require an `X-Fabric-Token` header
matching a Secret (`FABRIC_SIDECAR_TOKEN`, constant-time compare);
`/healthz` stays open for probes. Recommended whenever the Service is
reachable beyond one namespace:

```sh
kubectl create secret generic fabric-nemo-token \
  --from-literal=token="$(openssl rand -hex 32)"
```

```yaml
auth:
  tokenSecret:
    name: fabric-nemo-token
    key: token
```

Unset (default) → behaviour unchanged. The token check applies only
to TCP requests; UDS callers remain governed by socket filesystem
permissions and are unaffected when the token is enabled.

## Network isolation

`networkPolicy.enabled` defaults to **true**: ingress is restricted to
same-namespace pods, egress to DNS only (plus operator-defined
`egressTo` for LLM-provider calls from the content-safety rail).
Unix-socket traffic, when wired independently of this chart, is
unaffected. Widen
`networkPolicy.ingressFrom` for cross-namespace callers — and set
`auth.tokenSecret` when you do.

## Key values

| Key | Default | Purpose |
|-----|---------|---------|
| `railsConfigMap.name` | `""` | Existing ConfigMap containing custom Colang rails. |
| `railsConfigMap.mountPath` | `/etc/fabric/rails` | Where the rails are mounted; passed via `--rails-config`. |
| `starterRails.enabled` | `true` | Package and mount the model-free starter when no custom ConfigMap is selected. |
| `literalFilter.enabled` | `true` | Pass `--enable-default-literal-filter`; required with the starter. |
| `allowPassthrough` | `false` | Explicit dev-only fail-open mode when no rails bundle is selected. |
| `auth.tokenSecret.name` | `""` | Secret holding the shared token for `/v1/*` auth (`X-Fabric-Token`). Empty → auth off. |
| `auth.tokenSecret.key` | `token` | Key within the token Secret. |
| `networkPolicy.enabled` | `true` | Ingress same-namespace only; egress DNS-only. |
| `service.port` | `8080` | Container + Service TCP port. |

## Readiness and latency

`GET /healthz` proves the process started and the configured engine
loaded. It does not run a representative policy decision or validate
credentials used later by custom model-backed rails.

The chart does not enforce or certify a P99 latency target. Operators
must qualify `/v1/check` with their selected rails, topology and load.
The shared-Deployment mode shipped here is for smoke tests and
development clusters.
