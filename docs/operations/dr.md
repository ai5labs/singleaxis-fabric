# Fabric Node recovery runbook

This runbook covers the OSS recorder only. The durable state is the Collector's
persistent sending queue; telemetry already accepted by a destination follows
that destination's backup and recovery process.

## Recovery objectives

Set customer-specific recovery objectives for:

- the maximum acceptable capture interruption;
- the maximum queue backlog and outage duration;
- the time required to restore authenticated ingress and egress; and
- the destination's retention and restore guarantees.

Fabric does not publish universal RPO or RTO numbers because storage class,
traffic volume, destination behavior, and cluster operations determine them.

## Before an incident

1. Pin the released image by digest and preserve its signed chart and SBOM.
2. Record the approved values digest, Secret references, certificate issuers,
   NetworkPolicy peers, storage class, destination, and connector manifests.
3. Size queue PVCs for the tested destination-outage window.
4. Monitor queue capacity, export failures, rejected spans, restarts, PVC state,
   certificate expiry, and destination health.
5. Test node drain, pod restart, destination outage, duplicate delivery, and
   credential rotation with the exact production configuration.

## Destination outage

- Keep Fabric Node running so the persistent queue can absorb the tested
  backlog.
- Do not enable debug output or weaken protection as a workaround.
- Restore the approved destination or approved failover route.
- Verify backlog drains and reconcile duplicate records at the destination.
- Preserve timestamps, configuration digest, and operational logs as incident
  evidence.

When the queue reaches capacity, the production configuration backpressures
OTLP senders. Upstream sender behavior then determines whether activity waits,
spools, or is lost; qualify that behavior explicitly.

## Fabric Node or cluster failure

1. Confirm the StatefulSet PVCs still exist; the chart retains them on delete
   and scale-down.
2. Restore cluster access, certificates, export credentials, and network paths.
3. Redeploy the same image digest and approved configuration against retained
   PVCs. Never mount one file-storage database into multiple replicas.
4. Verify readiness, queue recovery, destination delivery, and deduplication.
5. If a PVC is corrupt, preserve it for investigation before using the
   customer-approved storage restore procedure.

## Evidence boundaries

Fabric Node cannot prove that an arbitrary destination durably persisted a
record. Preserve destination-specific receipts, immutable storage evidence,
access logs, and retention records in the system responsible for them.
