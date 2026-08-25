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

# Regulated posture (EU AI Act high-risk): provision the five
# operator-owned Secrets listed below, then name a real HTTPS OTLP
# destination, explicit egress route, release verification key, and
# an operator-managed cert-manager ClusterIssuer named fabric-ca-issuer.
helm upgrade --install fabric . \
    --namespace fabric-system --create-namespace \
    --values profiles/eu-ai-act-high-risk.yaml \
    --set tenant.id=TENANT_UUID \
    --set otel-collector.exporter.endpoint=https://otlp.example.com \
    --set 'update-agent.config.trustedKeys[0].publicKey=BASE64_ED25519_PUBLIC_KEY' \
    --set 'otel-collector.networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32' \
    --set 'otel-collector.networkPolicy.exporterEgress.ports[0].protocol=TCP' \
    --set 'otel-collector.networkPolicy.exporterEgress.ports[0].port=443'
```

Create these Secrets in `fabric-system` before the rollout. Their values stay
outside Helm values, rendered ConfigMaps, and source control:

| Secret | Key | Purpose |
|---|---|---|
| `fabric-otel-receiver-tls` | `tls.crt`, `tls.key` | Server identity for both OTLP/gRPC and OTLP/HTTP receivers |
| `fabric-otel-client-ca` | `ca.crt` | CA used to verify client certificates at OTLP ingress |
| `fabric-presidio-tenant-key` | `tenant.key` | Tenant-specific HMAC material used by the embedded telemetry redactor |
| `fabric-otel-sampler-key` | `hmac_key` | 32-byte deterministic sampling key |
| `fabric-otel-export-auth` | `authorization` | Complete outbound authorization header value |

`203.0.113.10/32` is documentation-only. Replace it with the approved backend
or controlled egress-gateway CIDR that serves the exporter endpoint. Kubernetes
NetworkPolicy cannot match a DNS name or prove that a CIDR belongs to a URL.

The high-risk profile requires mTLS on both OTLP receivers. A verified client
certificate authenticates possession of a key chained to the configured CA;
it does not map the certificate subject to a Fabric tenant or replace tenant
authorization. The profile also renders Presidio as a container in the
Collector pod and connects it over a mode-0600 Unix socket. The umbrella's
standalone Presidio Deployment remains disabled; cross-pod Unix-socket provider
flags from older releases are no longer valid. Kubernetes can validate that a
Secret reference is syntactically present, but only the pod rollout proves that
each named Secret and key exists.

The high-risk profile also requires cert-manager and an existing issuer at
`update-agent.tls.certManager.issuerRef` (the shipped reference is the
`fabric-ca-issuer` `ClusterIssuer`). cert-manager, rather than Helm, owns the
admission webhook serving key and its rotation. Override the issuer reference
when your organization uses a namespaced `Issuer` or another approved PKI.

### Inspecting the rendered manifests (template / lint only)

For pre-install review (`helm template`, `helm lint`, compliance audit
of the rendered manifests), allow only the placeholder update signing key and
provide a non-secret test endpoint:

```bash
helm template fabric . \
    --namespace fabric-system \
    --values profiles/eu-ai-act-high-risk.yaml \
    --set tenant.id=00000000-0000-4000-8000-000000000000 \
    --set otel-collector.exporter.endpoint=https://otlp.invalid.example \
    --set update-agent.config.allowPlaceholderKey=true \
    --set 'otel-collector.networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32' \
    --set 'otel-collector.networkPolicy.exporterEgress.ports[0].protocol=TCP' \
    --set 'otel-collector.networkPolicy.exporterEgress.ports[0].port=443'
```

`allowPlaceholderKey` is for offline render review only. Do not carry it into a
release values file. The deployed containers still require the referenced
Secrets to exist before they become Ready.

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

The OSS Fabric provides the **Connect, Observe, and Relay spine**—decision and
interaction records, public contracts, privacy filtering, correlation, and
customer-selected OTLP delivery—plus optional Control and Assurance building
blocks. It does not turn the Collector queue into an evidence store or ship the
enterprise investigation and retention workflows. Those belong in a
customer-owned backend or the SingleAxis Platform's Management, Assurance, and
Governance planes.

If your team operates the collection infrastructure and builds its own audit
trail on the public contract, this distribution is sufficient. If you need the
Decision Graph and evidence lifecycle as a managed or privately deployed
product, that is the SingleAxis Platform.

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

| Concern | Current state (v0.7.x) | Pointer |
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
