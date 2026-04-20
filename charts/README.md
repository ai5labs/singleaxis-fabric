# charts/

Helm charts for deploying Fabric **Layer 1** (public, Apache-2.0). The
top-level umbrella chart is `fabric/`; subcharts for each Layer 1
component live under `fabric/charts/`.

Layer 2 subcharts (judge-workers, escalation-service, context-graph,
telemetry-bridge, nats) live in a separate SingleAxis-internal repo
during Phase 1 and are not part of this distribution. See
[`../specs/012-oss-commercialization-strategy.md`](../specs/012-oss-commercialization-strategy.md).

## Authoritative spec

[`../specs/008-deployment-model.md`](../specs/008-deployment-model.md)

## Status

Pre-alpha — umbrella scaffolded with three Layer 1 subcharts:

- [`fabric/charts/otel-collector/`](./fabric/charts/otel-collector) — telemetry processors
- [`fabric/charts/nemo-sidecar/`](./fabric/charts/nemo-sidecar) — NeMo Colang inline guardrails
- [`fabric/charts/langfuse/`](./fabric/charts/langfuse) — local observability UI

Two profiles ship: [`permissive-dev`](./fabric/profiles/permissive-dev.yaml)
(dev clusters only) and [`eu-ai-act-high-risk`](./fabric/profiles/eu-ai-act-high-risk.yaml).

## Usage (once released)

```bash
helm repo add singleaxis https://charts.singleaxis.com
helm install fabric singleaxis/fabric \
    --namespace fabric-system --create-namespace \
    --values singleaxis/profiles/eu-ai-act-high-risk.yaml \
    --set tenant.id=<uuid> \
    --set tenant.vault.address=<url> \
    --set tenant.kms.keyArn=<arn>
```

## Chart structure

See [`../specs/008-deployment-model.md`](../specs/008-deployment-model.md)
for the planned layout and profile system.

## Release signing

Charts are signed with `cosign` and a `.prov` provenance file.
Verification instructions ship with each release.
