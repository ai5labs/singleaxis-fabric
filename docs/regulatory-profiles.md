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
| **Intended for** | local development, CI smoke tests, demos | production deployments under EU AI Act Annex III (high-risk) |
| **Production safe?** | **No** | Yes (with your signing keys + KMS) |
| **Presidio redaction** | ON (HMAC) | ON (HMAC, real recognizer set, fail-loud on missing key) |
| **NeMo guardrails** | optional | ON, output rails enforced |
| **OPA policy at 4 points** | optional | Required at all 4 points |
| **Egress allowlist (`fabricguard`)** | warn-only | enforce |
| **Sampling (`fabricsampler`)** | 100% | HMAC-keyed tail sampling per event class |
| **Tail-sample of denials/blocks** | retain | retain (regulator wants the bad cases) |
| **Network policies** | none | strict — sidecars cannot egress to public internet |
| **PodDisruptionBudgets** | none | yes |
| **Resource requests/limits** | minimal | production-sized |
| **Update-agent signing key** | placeholder allowed | real key required (chart fails to install otherwise) |
| **Exporter endpoint** | optional | required (chart fails to install otherwise) |
| **Tenant HMAC key** | dev placeholder | real Secret required |
| **Locked fields** | none | `otel-collector.fabric.guard.enabled`, `otel-collector.fabric.guard.dropUnknownClasses`, `otel-collector.fabric.redact.enabled` |

## Install

```bash
# permissive-dev — the kind-quickstart default
helm upgrade --install fabric oci://ghcr.io/singleaxis/charts/fabric \
  --values profiles/permissive-dev.yaml

# eu-ai-act-high-risk — production posture
helm upgrade --install fabric oci://ghcr.io/singleaxis/charts/fabric \
  --values profiles/eu-ai-act-high-risk.yaml \
  --set tenant.id=acme-corp \
  --set update-agent.config.signingKeySecret=acme-update-agent-key \
  --set presidio-sidecar.tenantKeySecret=acme-tenant-hmac \
  --set otel-collector.exporter.endpoint=https://otel.acme.example
```

## Fail-loud guards

The `eu-ai-act-high-risk` profile uses Helm `fail` templates rather than
hidden defaults. Missing-on-purpose to force a conscious deployer choice:

| Guard | Override (only for dry-render / kind) |
|---|---|
| `tenant.id` is set | `--set tenant.id=...` |
| Real update-agent signing key | `--set update-agent.config.allowPlaceholderKey=true` |
| Presidio redact-socket provider | `--set otel-collector.fabric.redact.acceptMissingProvider=true` |

If a guard fires, the failure message tells you exactly which field and
why. Don't bypass guards in production.

The exporter endpoint is **no longer** one of these guards. An unset
`otel-collector.exporter.endpoint` renders the debug exporter and
routes spans to the collector pod's stdout with a loud post-install
warning, rather than failing the render — so the former
`exporter.acceptUnsetEndpoint` escape hatch is gone. For a regulated
profile that is still the wrong end state: set a real endpoint, since
stdout is neither durable nor an audit trail.

## Deriving a custom profile

Profiles are just YAML files under `charts/fabric/profiles/`. Copy one
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

The `eu-ai-act-high-risk` profile locks three controls:

```yaml
lockedFields:
  - otel-collector.fabric.guard.enabled
  - otel-collector.fabric.guard.dropUnknownClasses
  - otel-collector.fabric.redact.enabled
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
   `drop_unknown_classes != true`, or `fabricredact` missing). The
   value supports `auto | on | off`; `auto` (the default) switches on
   when the release namespace carries the parent chart's
   `singleaxis.com/profile=eu-ai-act-high-risk` label. Note the
   first-install caveat: on a brand-new namespace the label isn't
   visible at render time yet, so `auto` engages from the first
   `helm upgrade` onward — set `on` to pin it from day one. Scope is
   limited to the webhook's `watchedNamespaces` (VWC
   namespaceSelector).

A tenant who genuinely wants different behaviour should switch to a
less strict profile rather than fight these checks.

## What the profile does *not* do

- It does not provision your IdP, KMS, or HSM. Those stay in your control.
- It does not enable the Commercial plane. To ship sanitized evidence to
  SingleAxis-hosted services, set `commercial.enabled=true` and provide a
  license key.
- It does not authorize tool calls — that's wired by your application
  via the `ToolAuthorizer` protocol. The profile only enforces *that
  the call happens*.
