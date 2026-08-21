# Deployment

Fabric's deployable unit is the umbrella Helm chart at
[`charts/fabric/`](../charts/fabric/). The authoritative spec for the
deployment model is
[`specs/008-deployment-model.md`](../specs/008-deployment-model.md);
this page is a pointer and a posture statement.

## Chart + profiles

```bash
cd charts/fabric
helm dependency build

# Dev / evaluation cluster:
helm install fabric . \
    --namespace fabric-system --create-namespace \
    --values profiles/permissive-dev.yaml

# Regulated workloads (EU AI Act high-risk):
#   - REPLACE the trustedKey publicKey with the real release Ed25519
#     public key (base64). The chart fails-closed otherwise.
#   - PROVIDE a Presidio sidecar. The umbrella bundles the subchart,
#     but this profile does not enable it (it needs a real tenant HMAC
#     key). Either enable it — `--set presidioSidecar.enabled=true`
#     plus `--set presidio-sidecar.tenantKey.existingSecret=<secret>` —
#     or point redact.existingSocketProvider at a sidecar you manage
#     yourself.
helm install fabric . \
    --namespace fabric-system --create-namespace \
    --values profiles/eu-ai-act-high-risk.yaml \
    --set tenant.id=<uuid> \
    --set update-agent.config.trustedKeys[0].publicKey=<real-base64-Ed25519-key> \
    --set otel-collector.fabric.redact.existingSocketProvider=<presidio-sidecar-name>
```

### Inspecting the rendered manifests (template / lint only)

For pre-install review (`helm template`, `helm lint`, compliance audit
of the rendered manifests), bypass the install-time checks:

```bash
helm template fabric . \
    --values profiles/eu-ai-act-high-risk.yaml \
    --set update-agent.config.allowPlaceholderKey=true \
    --set otel-collector.fabric.redact.acceptMissingProvider=true
```

Both flags **only affect template rendering**. The deployed binaries
re-validate at startup and refuse to run with a placeholder key or a
missing redact socket — a real `helm install` cannot bypass either
check even if the renderer was told to.

Two regulatory profiles ship today:

- `permissive-dev` — local / evaluation / non-regulated. Loose
  sampling, no retention constraints.
- `eu-ai-act-high-risk` — EU AI Act high-risk systems. Deny-default
  NetworkPolicy, tightened guardrail chain, pinned sampling, red-team
  cadence. Judge workers and the escalation service are **not** in this
  distribution; the profile configures only the OSS substrate.

Other profiles (NIST AI RMF, ISO 42001, SR 11-7, HIPAA) land
profile-by-profile as rubric content does. See the chart
[`README.md`](../charts/fabric/README.md) for current subchart
inventory (shipped vs. Phase 2).

## Compose (local smoke only)

[`deploy/compose/`](../deploy/compose/) provides a docker-compose
topology for local smoke testing the SDK + sidecars + OTel Collector
chain without a cluster. It is **not** a supported production
topology. Use Helm for anything that touches real traffic.

## What this OSS distribution covers

The OSS Fabric provides the **collection infrastructure** and the
**inline control plane** — decision spans, guardrail events,
escalation records, retrieval hashes, fail-loud guardrail sidecars,
the human-in-the-loop primitive. It does not generate evidence
bundles, signed audit trails, or regulator-shaped mappings; those
are produced by the SingleAxis commercial control plane (Context
Graph, evidence builder, escalation service, judge workers) layered
on top of this collection layer.

If your team operates the collection infrastructure yourselves and
builds your own audit trail on top of it, this distribution is
sufficient. If you need the audit trail itself as a managed product,
that's the SingleAxis control plane.

Fabric does not issue certifications either way: no SOC 2 report,
no ISO/IEC 42001 certificate, no EU AI Act conformity marking comes
out of installing this chart. Certification remains the tenant's
process; Fabric (with or without the commercial layer on top) is
what makes the evidence collection automatic.

## Operational posture

### Break-glass: disabling the admission webhook

If the update-agent webhook is down or misbehaving and is blocking
cluster operations (`failurePolicy: Fail` rejects admission when the
webhook is unreachable), you can flip it to fail-open **directly on
the ValidatingWebhookConfiguration**. Helm does not intercept this
object at apply time in a way that conflicts with `kubectl patch` —
patching it takes effect immediately:

```bash
# Find the VWC (named <release>-update-agent):
kubectl get validatingwebhookconfiguration -l app.kubernetes.io/part-of=fabric

# Flip to Ignore (admission proceeds when the webhook can't answer):
kubectl patch validatingwebhookconfiguration <release>-update-agent \
  --type=merge \
  -p '{"webhooks":[{"name":"<release>-update-agent.fabric.singleaxis.dev","failurePolicy":"Ignore"}]}'
```

Rules of engagement:

- **Time-box it.** Note the timestamp; the next `helm upgrade` of the
  chart restores `failurePolicy: Fail` from values
  (`update-agent.webhook.failurePolicy`). To restore sooner, patch
  back to `"Fail"` manually.
- **Audit it.** While `Ignore` is set, unsigned/tampered channel
  manifests are admitted silently. Export the audit log for the
  window (`kubectl get events --field-selector
  involvedObject.kind=ValidatingWebhookConfiguration`) and note who
  patched.
- **Never edit the rendered manifest instead** — a `helm upgrade`
  will reconcile over it without telling you the escape hatch was
  open.

| Concern | Current state (v0.6.x) | Pointer |
|---------|------------------------|---------|
| Disaster recovery | Stateless components recoverable from Git; stateful services (Postgres, NATS) follow standard backup practice. A DR runbook ships; the wider runbook set (upgrade, rollback, key rotation, collector backpressure) does not yet | [`operations/dr.md`](operations/dr.md) |
| Upgrade channel | Manual `helm upgrade`. The signed-manifest Update Agent ships as an opt-in subchart (`updateAgent.enabled=true`, requires a real Ed25519 signing key) | Chart [`README`](../charts/fabric/README.md) |
| High availability | `profile.availability: ha` opt-in (3-node NATS, replicated Postgres, ≥2 worker replicas) | [`specs/008-deployment-model.md`](../specs/008-deployment-model.md) |
| Image signing | Cosign (keyless via Fulcio), SLSA build provenance, SBOM shipped from `0.1.0`; Helm `.prov` on roadmap | [`SECURITY.md`](../SECURITY.md) §Release signing |

## Roadmap / not yet shipping

Helm `.prov` provenance files (cosign signing of OCI charts is the
current path); NIST RMF / ISO 42001 / SR 11-7 / HIPAA profiles;
Decision Graph and Telemetry Bridge subcharts. See the chart README
and spec 008 for current Phase 2 scope.

The umbrella chart itself **does** publish as a cosign-signed OCI
artifact at `oci://ghcr.io/singleaxis/charts/fabric` on each release.
Subcharts are bundled inside it and are not published or installable
on their own.
