# Upgrade from Fabric chart v0.6 to v0.7

Fabric v0.7 introduces JSON Schema validation for the umbrella chart and the
Fabric OpenTelemetry Collector. Run the new chart against the exact values
file used by each environment before changing a release:

```bash
helm lint charts/fabric --values /secure/path/production-values.yaml
helm template fabric charts/fabric \
  --namespace fabric-system \
  --values /secure/path/production-values.yaml >/dev/null
```

Schema rejection is a pre-deployment safety check. A misspelled or obsolete
control is now an error instead of an ignored value that may leave a weaker
runtime posture.

## Required redaction migration

The following v0.6 Collector values were removed:

- `fabric.redact.existingSocketProvider`
- `fabric.redact.acceptMissingProvider`

They described a Unix socket provider outside the Collector pod. Kubernetes
pods cannot share a Unix socket merely by naming another workload, so those
values could produce a deployment that claimed redaction but had no usable
provider. v0.7 rejects both keys.

Migrate to the verified pod-local topology:

```yaml
otel-collector:
  fabric:
    redact:
      enabled: true
      unixSocket: /var/run/fabric/presidio.sock
      byteHandling: redact_utf8
      embedded:
        enabled: true
        tenantKeySecret:
          name: fabric-presidio-tenant-key
          key: tenant.key
        redactionMode: hmac
```

Provision `fabric-presidio-tenant-key` before the rollout. The chart mounts a
shared pod-local volume and renders the redactor alongside the Collector; it
does not place secret material in Helm values.

## High-risk ingress and egress migration

The high-risk profile now requires verified mTLS on both OTLP receivers. Create
the profile's server identity and trusted client CA Secrets before upgrading:

```bash
kubectl -n fabric-system create secret tls fabric-otel-receiver-tls \
  --cert=/secure/path/receiver.crt \
  --key=/secure/path/receiver.key
kubectl -n fabric-system create secret generic fabric-otel-client-ca \
  --from-file=ca.crt=/secure/path/client-ca.crt
```

The profile supplies these Secret references and locks
`receiver.requireTLS` and `receiver.requireClientCertificate`. Certificate
material remains in Kubernetes Secrets; only mounted file paths appear in the
Collector configuration. Client-certificate verification authenticates a
certificate chain, but does not map certificate subjects to Fabric tenant
authorization.

The old high-risk in-namespace egress peer was removed. It could render
successfully while silently blocking an external exporter. A high-risk render
now fails until the operator supplies an explicit exporter peer and port:

```yaml
otel-collector:
  networkPolicy:
    exporterEgress:
      to:
        - ipBlock:
            cidr: 192.0.2.10/32 # replace with the real controlled route
      ports:
        - protocol: TCP
          port: 443
```

NetworkPolicy has no DNS-name peer. Prefer a controlled egress gateway when a
SaaS backend's addresses change. The render check proves that a peer and port
were supplied; it cannot prove that they correspond to `exporter.endpoint` or
that the cluster CNI enforces NetworkPolicy.

## Compatible and behavior-changing values

The v0.6 values exercised by
[`../tests/fixtures/v0.6-supported-values.yaml`](../tests/fixtures/v0.6-supported-values.yaml)
remain accepted. Export endpoints, guard settings, policy settings, sampler
keys/rates, debug settings, replica counts, and NetworkPolicy peers retain
their previous names.

Two defaults changed and should be reviewed even though they do not require a
key rename:

- The umbrella Collector is enabled by default so a successful installation
  cannot be an empty telemetry deployment.
- Deterministic sampling is disabled by default. This preserves the complete
  stream unless an operator deliberately supplies a stable HMAC key and rates.

v0.7 also adds authenticated TLS ingress/export and a durable queue. Existing
non-profile values continue to use plaintext local ingress and the in-memory
queue defaults. Regulated environments should enable `receiver.requireTLS`,
`receiver.requireClientCertificate`, `exporter.requireTLS`,
`exporter.requireAuth`, and `exporter.requireDurableQueue` and satisfy their
fail-closed render checks.

## Rollout and rollback

1. Render with production values and resolve every schema error.
2. Confirm the receiver certificate, client CA, destination credential,
   redaction key, StorageClass, and explicit NetworkPolicy egress exist before
   upgrade.
3. Use `helm upgrade --atomic --wait` and retain the prior release revision.
4. Confirm Collector queue and export failure metrics before declaring the
   rollout healthy. Process readiness does not prove backend delivery.
5. If rollback is required, preserve queue PVCs. A rollback to v0.6 cannot use
   the new embedded-redactor values and reintroduces the unsafe redaction
   topology; treat that as a temporary loss of the v0.7 assurance claim.
