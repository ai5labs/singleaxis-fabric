# Fabric Relay Helm chart

This chart installs the Fabric Relay as a security-hardened `StatefulSet`.
Every replica receives its own queue volume; queues are never shared between
Collector processes. The umbrella chart keeps Relay disabled until the complete
Collector-to-destination path has passed integration qualification.

The umbrella toggle only installs Relay. It does **not** automatically change
the Fabric Collector exporter endpoint or provision client certificates for
that hop. Collector-to-Relay routing and mTLS identity remain an explicit
integration step and are not yet accepted as a regulated end-to-end profile.

## Production install

Create credentials outside Helm values:

```bash
kubectl -n fabric-system create secret generic fabric-relay-auth \
  --from-literal=authorization='Bearer replace-me'
```

Keep the credential out of shell history in a real deployment by creating the
Secret through the organization's secret manager or GitOps secret controller.

```yaml
mode: production
receiver:
  tls:
    serverCertificateSecret:
      name: fabric-relay-server-tls
    clientCASecret:
      name: fabric-relay-client-ca
destination:
  endpoint: https://otlp.example.com
  allowedEndpoints:
    - https://otlp.example.com
  auth:
    secretRef:
      name: fabric-relay-auth
      key: authorization
persistence:
  enabled: true
  size: 100Gi
  storageClass: encrypted-rwo
debugExporter:
  enabled: false
networkPolicy:
  enabled: true
  ingressFrom:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: fabric-system
      podSelector:
        matchLabels:
          app.kubernetes.io/name: otel-collector
  egressTo:
    - ipBlock:
        cidr: 203.0.113.0/24 # replace with the approved destination range
```

```bash
helm upgrade --install fabric-relay ./charts/fabric/charts/fabric-relay \
  --namespace fabric-system --create-namespace -f production-values.yaml
```

Production mode rejects a missing or non-HTTPS destination, a destination not
exactly present in `allowedEndpoints`, insecure TLS, ephemeral queue storage,
finite retry windows, and debug export. Authentication and mTLS credentials are
accepted only through references to pre-existing Kubernetes Secrets.
Destination URLs containing userinfo, query parameters or fragments are
rejected so credentials cannot be smuggled into the rendered ConfigMap or Helm
release. Both receiver protocols require client certificates in production.
Production also requires NetworkPolicy with at least one explicit ingress
selector and one explicitly restricted egress peer or CIDR. Empty peers, empty
label selectors, `0.0.0.0/0`, and `::/0` are rejected. In every mode, an
enabled policy with an empty `ingressFrom` denies all ingress rather than
allowing the cluster.

`allowedEndpoints` prevents configuration drift at Helm render time. It is not
a network security boundary. Enforce destination IP ranges using
`networkPolicy.egressTo` and `networkPolicy.destinationPort`, an egress gateway,
service mesh, or enterprise firewall. Kubernetes NetworkPolicy cannot securely
allowlist DNS names.

## Development mode

The default standalone render is deliberately obvious and safe for evaluation:
it receives OTLP and writes it to the debug exporter. It does not pretend pod
logs or `emptyDir` storage are durable evidence. No destination is contacted by
default.

## Delivery and scaling

Relay provides at-least-once delivery after queue acceptance. Duplicate events
are possible after retries, so receivers must deduplicate using Fabric event
identity. `block_on_overflow` applies backpressure when the configured queue is
full rather than intentionally discarding the newest records.

Each StatefulSet replica owns an independent PVC. Scale-down retains PVCs by
default. Before scaling down a replica, verify that its queue is drained; a
retained PVC is not actively processed after its pod is removed.

The queue and compaction directories are restricted to child paths beneath the
actual PVC mount, `/var/lib/fabric-relay`. Paths rooted elsewhere, paths with
traversal segments, and using the same directory for both roles fail at render
time. This prevents a deployment from appearing durable while file storage is
actually writing to the read-only image or an ephemeral filesystem.

## Cluster qualification: destination outage and recovery

Template tests prove configuration shape, not runtime durability. Qualify each
storage class, Kubernetes distribution, Relay image and destination combination:

1. Install production mode against a controlled OTLP receiver with one replica.
2. Record the Relay pod UID, PVC UID and baseline `otelcol_exporter_queue_size`.
3. Make the destination unavailable without terminating Relay.
4. Send a numbered set of OTLP events and confirm queue size grows.
5. Delete the Relay pod and confirm the replacement mounts the same PVC and the
   queue remains non-zero.
6. Restore the destination and wait for the queue to drain.
7. Verify every event identity arrived at least once; document duplicates.
8. Repeat during a node drain and a chart upgrade.
9. Archive manifests, image digest, storage class, metrics and receiver records
   as qualification evidence.

Do not claim production persistence until this procedure passes in the target
environment.

## Operational signals

Scrape port `8888`. At minimum, alert on exporter send failures, queue capacity
and size, enqueue failures, refused telemetry, process memory, pod restarts and
PVC saturation. Readiness indicates process/pipeline health; it does not prove
that the remote destination is available.
