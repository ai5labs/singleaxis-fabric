# Regulatory profiles

Fabric ships two named umbrella-chart profiles. They are *opinionated*
default-value bundles for the umbrella `charts/fabric` chart — same
components, different posture.

> Profiles set defaults; every value remains overridable **except
> the fields a profile locks** (see [Locked-field
> enforcement](#locked-field-enforcement) below) — overriding one of
> those fails the render.

| | `permissive-dev` | `eu-ai-act-high-risk` |
|---|---|---|
| **Intended for** | local development, CI smoke tests, demos | customer-qualified high-risk deployments |
| **Compliance claim** | none | none; technical posture only |
| **Telemetry PII redaction** | off | embedded Presidio, fail-loud Secret reference |
| **Collector allowlist/policy** | development defaults | guard, trace processing, reference egress policy, and redaction locked on |
| **OTLP receiver** | plaintext development ingress | server TLS plus verified client certificates |
| **Exporter** | debug stdout unless configured | HTTPS, Secret-backed auth, durable queue, unlimited transient retry |
| **Network policy** | deny-default off | namespace deny-default plus explicit exporter peer/port |
| **Sampling** | fixed development key | operator Secret-backed deterministic key |
| **Availability** | one replica | two replicas, PDB, retained per-replica queue PVC |
| **Update verification** | development posture | fail-closed admission with a real trusted key |
| **Locked fields** | none | security posture fields listed in the profile |

## Install

```bash
# permissive-dev — the kind-quickstart default
helm upgrade --install fabric oci://ghcr.io/singleaxis/charts/fabric \
  --values profiles/permissive-dev.yaml

# eu-ai-act-high-risk — production posture
helm upgrade --install fabric oci://ghcr.io/singleaxis/charts/fabric \
  --namespace fabric-system --create-namespace \
  --values profiles/eu-ai-act-high-risk.yaml \
  --set tenant.id=TENANT_UUID \
  --set 'update-agent.config.trustedKeys[0].publicKey=BASE64_ED25519_PUBLIC_KEY' \
  --set otel-collector.exporter.endpoint=https://otel.acme.example \
  --set 'otel-collector.networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32' \
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].protocol=TCP' \
  --set 'otel-collector.networkPolicy.exporterEgress.ports[0].port=443'
```

Replace the documentation CIDR with an approved backend or controlled
egress-gateway peer. Provision the five Secrets listed in
[`deployment.md`](deployment.md), install cert-manager, and provision the
configured webhook issuer before rollout. Webhook certificate issuance and
rotation stay with that operator-managed PKI; the profile does not put a
generated private key into Helm release state.

## Fail-loud guards

The `eu-ai-act-high-risk` profile uses schema and Helm failures instead of
inventing deployment-specific values. It requires a tenant ID, HTTPS exporter,
Secret-backed exporter auth, persistent queue, receiver mTLS, explicit exporter
NetworkPolicy peer/ports, and a real update verification key. It also pins the
update admission controller on, fail-closed, with cert-manager TLS. The only
dry-render exception is `update-agent.config.allowPlaceholderKey=true`; it must
never be stored in production values. Missing Kubernetes Secret objects are
detected by the pod rollout, because Helm can validate references but cannot
read or prove operator-owned secret data.

## Deriving a custom profile

Profiles are plain YAML files under `charts/fabric/profiles/`. Copy one
and edit:

```bash
cp charts/fabric/profiles/eu-ai-act-high-risk.yaml profiles/acme-finance.yaml
# edit profile.name, profile.regulations, lockedFields, and any value overrides
helm upgrade fabric oci://ghcr.io/singleaxis/charts/fabric \
  --values profiles/acme-finance.yaml
```

A few good practices:

- **Lock fields you don't want operators to change** with the
  `profile.lockedFields` list — see
  [Locked-field enforcement](#locked-field-enforcement) for what the
  chart actually does with it. Only boolean enable-flags are supported
  as locks today: a locked field must stay `true`.
- **Set `profile.regulations`** so the `fabricsampler` retains the
  right event classes (e.g. anything tagged `eu-ai-act` keeps 100%
  sampling).
- **Always set the exporter endpoint**, even if your backend is in-cluster
  — `accept` flags are for `helm template` only.

## Locked-field enforcement

The `eu-ai-act-high-risk` profile locks the complete security posture:

```yaml
lockedFields:
  - networkPolicy.denyDefault
  - otel-collector.fabric.guard.enabled
  - otel-collector.fabric.guard.dropUnknownClasses
  - otel-collector.fabric.guard.traceProcessingEnabled
  - otel-collector.fabric.redact.enabled
  - otel-collector.receiver.requireTLS
  - otel-collector.receiver.requireClientCertificate
  - otel-collector.exporter.requireEndpoint
  - otel-collector.exporter.requireTLS
  - otel-collector.exporter.requireAuth
  - otel-collector.exporter.requireDurableQueue
  - otel-collector.exporter.sendingQueue.persistence.enabled
  - otel-collector.networkPolicy.enabled
  - otel-collector.networkPolicy.exporterEgress.requireExplicit
  - update-agent.config.failClosed
  - update-agent.networkPolicy.enabled
```

Helm merges profile values and user `--set` overrides **before**
rendering, so a template cannot tell a profile default from a tenant
override. Enforcement is therefore keyed on profile identity, in two
layers:

1. **Render-time invariant (primary).** The parent chart's
   `fabric.validateProfileLocks` helper (invoked from
   `templates/namespace.yaml`) fails the render whenever a profile
   with a non-empty `lockedFields` list resolves any locked path to
   false, or when the owning component is toggled off entirely
   (`otelCollector.enabled=false`). The error names the disabled
   control. This fires on `helm install`, `helm upgrade`, and
   `helm template`.

   ⚠️ `helm lint` executes the check but **always exits 0** — it logs
   the failure as an INFO line and reports "0 chart(s) failed"
   (verified on Helm 3.19; it swallows subchart fails the same way).
   Never use lint as your enforcement gate; use `helm template`,
   install, or upgrade.

2. **Admission backstop (secondary).** After install, direct
   `kubectl edit`/`apply` drift bypasses Helm entirely. When
   `update-agent.webhook.enforceProfileLocks` is active, the
   update-agent verifier denies any ConfigMap carrying the
   otel-collector config naming whose rendered collector config drops
   a locked control (`fabricguard` missing,
   `drop_unknown_classes != true`, `fabricredact` missing, or either
   processor absent from the active logs/traces pipelines). The
   value supports `auto | on | off`; `auto` (the default) switches on
   when the release namespace carries the parent chart's
   `singleaxis.com/profile=eu-ai-act-high-risk` label. Note the
   first-install caveat: on a brand-new namespace the label isn't
   visible at render time yet, so `auto` engages from the first
   `helm upgrade` onward. The shipped EU profile therefore pins this
   value to `on` from day one; custom high-risk profiles should do the
   same. Scope is
   limited to the webhook's `watchedNamespaces` (VWC
   namespaceSelector).

A tenant who genuinely wants different behaviour should switch to a
less strict profile rather than fight these checks.

## What the profile does *not* do

- It does not provision your IdP, KMS, or HSM. Those stay in your control.
- It does not enable a hidden platform connection. Configure the Relay exporter
  explicitly for a customer backend or approved SingleAxis Platform endpoint.
- It does not authorize tool calls. Application or gateway code must invoke and
  enforce the selected `ToolAuthorizer`; the Collector can record and filter
  authorization events only after they are emitted.
- It does not map mTLS certificate subjects to Fabric tenants. That workload
  identity binding remains a deployment/controller responsibility.
