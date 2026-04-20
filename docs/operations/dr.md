# Disaster recovery

Skeleton. This page states the DR posture; detailed runbooks land
with the Phase 2 operational surface.

## Posture

Fabric's deployable components split into two groups:

| Group | Components | Recovery approach |
|-------|------------|-------------------|
| **Stateless** | OTel Collector, guardrail sidecars (Presidio, NeMo), judge workers, escalation service API, update agent admission webhook, admin UI | Redeploy from the Helm chart. No persistent state to restore. Idempotent and bootstrap-able from Git. |
| **Stateful** | Postgres (Context Graph, escalation service), NATS JetStream (telemetry bridge queues), Langfuse datastore | Restore from the tenant's backup channel. WAL / stream snapshots feed the tenant's standard object-storage backup regime. |

The Fabric chart ships an init Job
([`components/langfuse-bootstrap/`](../../components/langfuse-bootstrap/))
that seeds Langfuse with the curated project shape on a clean
install. Re-running the chart against a clean Langfuse replays
that bootstrap idempotently.

## Recovery steps (outline)

1. Provision a Kubernetes cluster in the recovery region.
2. Restore Postgres and NATS from the most recent snapshots.
3. `helm install fabric` against the restored datastores, pinning
   the exact chart version that was running pre-incident.
4. Validate that the collector is receiving agent spans, judge
   workers are draining, and the escalation service can resume
   paused decisions.

Detailed runbooks — snapshot cadence, validation checks, tenant
cutover procedure — are a Phase 2 deliverable.

## Pointers

- Chart and subcharts: [`charts/fabric/`](../../charts/fabric/)
- Bootstrap Job: [`components/langfuse-bootstrap/`](../../components/langfuse-bootstrap/)
- Deployment model (source of record):
  [`specs/008-deployment-model.md`](../../specs/008-deployment-model.md)
- Deployment doc: [`../deployment.md`](../deployment.md)

## Roadmap / not yet shipping

Detailed per-profile runbooks, tenant-facing backup validation
tooling, and automated multi-region failover are Phase 2. Until
those land, DR stays a tenant-owned operational responsibility:
Fabric provides recoverable components; the tenant's SRE owns
the backup schedule, restore drills, and cutover decisions.
