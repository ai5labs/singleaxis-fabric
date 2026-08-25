# Fabric OpenTelemetry Collector chart

This chart deploys the SingleAxis Fabric Collector distribution with the
`fabricguard`, `fabricpolicy`, `fabricredact`, and `fabricsampler`
processors. It can send logs and traces to any OTLP/HTTP backend. No
SingleAxis SaaS connection is required.

## Authoritative references

- [`../../../../specs/004-telemetry-bridge.md`](../../../../specs/004-telemetry-bridge.md)
- [`../../../../specs/008-deployment-model.md`](../../../../specs/008-deployment-model.md)
- [`../../../../components/otel-collector-fabric/`](../../../../components/otel-collector-fabric/)

## Development install

A bare install has no invented backend. It writes accepted telemetry to
the Collector's `debug` exporter and prints a warning in Helm NOTES:

```bash
helm install fabric-otelcol charts/fabric/charts/otel-collector \
  --namespace fabric-system --create-namespace
```

Pod stdout is not durable and is not an audit trail. Configure a backend
before using the chart outside development.

## Regulated ingress and delivery install

Create the receiver identity, trusted client CA, and egress credential outside
Helm. Secret values are mounted or injected into the pod and never copied into
values or the Collector ConfigMap:

```bash
kubectl -n fabric-system create secret tls fabric-otel-receiver-tls \
  --cert=/secure/path/receiver.crt \
  --key=/secure/path/receiver.key
kubectl -n fabric-system create secret generic fabric-otel-client-ca \
  --from-file=ca.crt=/secure/path/client-ca.crt
kubectl -n fabric-system create secret generic fabric-otel-export-auth \
  --from-file=authorization=/secure/path/to/authorization-header
```

Then render the hardened delivery posture:

```bash
helm upgrade --install fabric-otelcol charts/fabric/charts/otel-collector \
  --namespace fabric-system --create-namespace \
  --set receiver.requireTLS=true \
  --set receiver.requireClientCertificate=true \
  --set receiver.tls.serverCertificateSecret.name=fabric-otel-receiver-tls \
  --set receiver.tls.clientCASecret.name=fabric-otel-client-ca \
  --set exporter.endpoint=https://otlp.example.com \
  --set exporter.requireEndpoint=true \
  --set exporter.requireTLS=true \
  --set exporter.insecure=false \
  --set exporter.requireAuth=true \
  --set exporter.auth.secret.name=fabric-otel-export-auth \
  --set exporter.requireDurableQueue=true \
  --set exporter.sendingQueue.persistence.enabled=true \
  --set exporter.sendingQueue.persistence.fsync=true \
  --set exporter.sendingQueue.blockOnOverflow=true \
  --set exporter.retry.maxElapsedTime=0s \
  --set networkPolicy.enabled=true \
  --set networkPolicy.exporterEgress.requireExplicit=true \
  --set 'networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32' \
  --set 'networkPolicy.exporterEgress.ports[0].protocol=TCP' \
  --set 'networkPolicy.exporterEgress.ports[0].port=443'
```

`203.0.113.10/32` is a documentation-only address; replace it with the
controlled egress gateway or backend CIDR that actually serves the exporter
endpoint. Kubernetes NetworkPolicy does not accept DNS names and the chart
cannot prove that a supplied CIDR corresponds to `exporter.endpoint`. Backends
with changing public IPs should be reached through a controlled egress gateway.

When the backend uses a private CA, create a Secret containing `ca.crt`
and set `exporter.tls.caSecret.name`. For mTLS, create a TLS Secret and
set `exporter.tls.clientCertificateSecret.name`. Secret data is mounted
read-only; it is never embedded in the rendered Collector configuration.

The EU high-risk umbrella profile locks receiver mTLS, endpoint TLS, auth,
durable queue, NetworkPolicy, and explicit exporter egress requirements on.
An operator still supplies the actual HTTPS endpoint, egress peer/port, Secret
objects, and an encrypted Kubernetes StorageClass. The chart cannot prove that
a storage provider encrypts PVCs at rest.

Both OTLP/gRPC and OTLP/HTTP receivers use the standard Collector server TLS
fields `cert_file`, `key_file`, and `client_ca_file`. Static chart tests verify
their rendered shape and Secret projection. The pinned Collector distribution
also passes `otelcol-fabric validate` with the rendered receiver mTLS and
`otlp_http` exporter configuration. Release qualification must repeat that
validation against each rebuilt artifact; a Helm render alone is not binary
acceptance evidence.

## Delivery contract

With persistence enabled, the chart switches from a `Deployment` to a
`StatefulSet`, attaches one retained PVC per replica, enables the OTel file
storage extension, and points the exporter's sending queue at it. An
`existingClaim` may be used only with one replica because multiple
Collectors must not share a bbolt queue database.

This is bounded **at-least-once transport**, not exactly-once delivery:

- A batch acknowledged by the backend is removed from the queue. A crash
  after the backend commits but before the acknowledgement is recorded can
  replay the batch, so downstream consumers must tolerate duplicates.
- Permanent export failures can be dropped. A full/unwritable PVC, storage
  corruption, invalid telemetry, or a sender that gives up under
  backpressure can also lose data.
- Guard, policy, redaction, and sampling processors can intentionally drop
  telemetry before it reaches the queue. Persistent delivery does not
  override those decisions.
- PVC deletion, StorageClass failure, or loss of encryption keys is outside
  the Collector's durability boundary. PVC retention is explicit, but
  backup and disaster recovery remain operator responsibilities.
- The queue is a delivery buffer, not the evidence store. The receiving
  platform remains responsible for immutable retention, indexing,
  provenance, and audit access.

Alert on the Collector exporter metrics, especially
`otelcol_exporter_enqueue_failed_spans`,
`otelcol_exporter_enqueue_failed_log_records`,
`otelcol_exporter_send_failed_spans`,
`otelcol_exporter_send_failed_log_records`,
`otelcol_exporter_queue_size`, and `otelcol_exporter_queue_capacity`.
The health endpoint proves that the Collector process and configuration
are live; it does not prove backend reachability or zero loss.

## Key values

| Key | Default | Purpose |
| --- | --- | --- |
| `fabric.guard.enabled` | `true` | Deny-by-default telemetry schema allowlist. |
| `fabric.redact.enabled` | `false` | Telemetry redaction using the embedded pod-local Presidio provider. |
| `fabric.policy.bundleConfigMap` | `""` | Operator-owned Rego bundle source. |
| `receiver.requireTLS` | `false` | Requires a Secret-backed server identity on both OTLP receivers. |
| `receiver.requireClientCertificate` | `false` | Requires a client CA and verified mTLS ingress. |
| `exporter.endpoint` | `""` | OTLP/HTTP destination; empty selects debug stdout only. |
| `exporter.requireTLS` | `false` | Requires HTTPS with verification enabled. |
| `exporter.auth.secret.name` | `""` | Secret supplying one outbound HTTP header value. |
| `exporter.sendingQueue.enabled` | `true` | Bounded exporter queue; in-memory unless persistence is enabled. |
| `exporter.sendingQueue.persistence.enabled` | `false` | Disk-backed queue and StatefulSet/PVC topology. |
| `exporter.retry.maxElapsedTime` | `5m` | Retry limit; `0s` retries transient failures indefinitely. |
| `exporter.requireDurableQueue` | `false` | Enforces persistent queue, fsync, backpressure, and unlimited transient retry. |
| `networkPolicy.exporterEgress.requireExplicit` | `false` | Requires an operator-supplied exporter peer and port rule. |

## Operational behavior

- The Collector and embedded redactor run as UID/GID 65532 with a
  read-only root filesystem, no privilege escalation, dropped capabilities,
  and a `RuntimeDefault` seccomp profile.
- The ServiceAccount token is not auto-mounted.
- Startup, readiness, and liveness probes use the OTel `health_check`
  extension on port 13133.
- Chart-managed queue PVCs use `ReadWriteOnce`, default to 10 GiB, and are
  retained when the StatefulSet is deleted or scaled down.
- NetworkPolicy cannot infer an external endpoint's IPs. A deny-default
  installation must explicitly allow DNS and the selected backend route.
- Client certificates authenticate possession of a key chained to the supplied
  CA. This chart does not map certificate subjects to Fabric tenants or replace
  application-level tenant authorization.

Run static qualification with:

```bash
helm lint charts/fabric/charts/otel-collector
./charts/fabric/tests/test-values-schema.sh
./charts/fabric/charts/otel-collector/tests/test-ingress-egress-security.sh
./charts/fabric/charts/otel-collector/tests/test-durable-delivery.sh
./charts/fabric/charts/otel-collector/tests/test-privacy-policy-wiring.sh
./charts/fabric/charts/otel-collector/tests/test-traces-pipeline.sh
```

`values.schema.json` rejects unknown keys within Fabric processors and exporter
delivery controls and validates their types, enums, rates, ranges, ports, and
durations. Kubernetes-native scheduling, resource, security-context, probe,
and NetworkPolicy peer objects remain extensible so Kubernetes API additions
do not require a Collector chart release. Upgrade-specific removals and the
N-1 compatibility fixture are documented in the umbrella chart's
[`upgrading-v0.6-to-v0.7.md`](../../docs/upgrading-v0.6-to-v0.7.md).
