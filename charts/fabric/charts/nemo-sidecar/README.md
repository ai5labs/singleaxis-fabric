# nemo-sidecar subchart

NeMo Colang guardrails sidecar. Spec:
[`specs/005-guardrails-inline.md`](../../../../specs/005-guardrails-inline.md).

## What this ships

A FastAPI sidecar that evaluates prompts and outputs against a Colang
rails configuration:

- `GET  /healthz`
- `POST /v1/check` — returns `{allow, reasons, rails_fired}`

## Phase 1 scope

- Shared `Deployment` + `Service` mode (TCP) for smoke tests and dev
  clusters. Production should inject the container as a per-agent-pod
  sidecar over a Unix domain socket for <100ms P99 — the
  sidecar-injection webhook lands in Phase 2.
- Passthrough engine (fail-open) when `railsConfigMap.name` is unset.
  Production profiles must supply a Colang rails ConfigMap.

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

Unset (default) → behaviour unchanged. Same-pod UDS callers are
unaffected either way.

## Network isolation

`networkPolicy.enabled` defaults to **true**: ingress is restricted to
same-namespace pods, egress to DNS only (plus operator-defined
`egressTo` for LLM-provider calls from the content-safety rail).
Same-pod UDS traffic rides loopback and is unaffected. Widen
`networkPolicy.ingressFrom` for cross-namespace callers — and set
`auth.tokenSecret` when you do.

## Key values

| Key | Default | Purpose |
|-----|---------|---------|
| `railsConfigMap.name` | `""` | ConfigMap containing the Colang rails. Unset → passthrough. |
| `railsConfigMap.mountPath` | `/etc/fabric/rails` | Where the rails are mounted; passed via `--rails-config`. |
| `auth.tokenSecret.name` | `""` | Secret holding the shared token for `/v1/*` auth (`X-Fabric-Token`). Empty → auth off. |
| `auth.tokenSecret.key` | `token` | Key within the token Secret. |
| `networkPolicy.enabled` | `true` | Ingress same-namespace only; egress DNS-only. Same-pod UDS unaffected. |
| `service.port` | `8080` | Container + Service TCP port. |

## Latency posture (published budget)

| Route | P99 target |
|-------|------------|
| `POST /v1/check` | <100ms (passthrough or Colang rails) |

Budgets apply to the sidecar process itself — colocated UDS calls
skip the TCP stack. The shared-Deployment mode shipped here is for
smoke-tests and dev clusters only.
