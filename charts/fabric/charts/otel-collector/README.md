# otel-collector (subchart)

Deploys the SingleAxis Fabric OpenTelemetry Collector distribution
— the OCB build with `fabricguard`, `fabricpolicy`, `fabricredact`,
and `fabricsampler` wired in. The chart is also installable
standalone, which is the expected path while the umbrella chart is
scaffolded.

## Authoritative refs

- [`../../../../specs/004-telemetry-bridge.md`](../../../../specs/004-telemetry-bridge.md)
- [`../../../../specs/008-deployment-model.md`](../../../../specs/008-deployment-model.md)
- [`../../../../components/otel-collector-fabric/`](../../../../components/otel-collector-fabric/)

## Install (standalone)

```bash
helm install fabric-otelcol charts/fabric/charts/otel-collector \
  --namespace fabric-system --create-namespace \
  --set image.repository=ghcr.io/singleaxis/fabric-otelcol \
  --set image.tag=0.1.0 \
  --set exporter.endpoint=http://fabric-ingest.fabric-system.svc:8080
```

## Key values

| Key | Default | Notes |
| --- | --- | --- |
| `image.repository` | `ghcr.io/singleaxis/fabric-otelcol` | Published image (tick 4). |
| `fabric.guard.enabled` | `true` | Deny-by-default schema allowlist. Keep on. |
| `fabric.policy.enabled` | `false` | When enabled, requires an existing Rego ConfigMap and absolute bundle path. |
| `fabric.redact.enabled` | `false` | When enabled, requires a co-located sidecar or explicit external socket volume. |
| `fabric.sampler.enabled` | `false` | When enabled, requires `hmacKey` OR `hmacKeySecret`. |
| `exporter.endpoint` | empty | Falls back loudly to debug/stdout unless the selected profile requires a durable endpoint. |

The chart refuses to render if `fabric.sampler.enabled=true` but
neither `hmacKey` nor `hmacKeySecret.name` is set — see
`_helpers.tpl`'s `otel-collector.validateSampler`.

When the inline `hmacKey` is used, it must be a 64-char lowercase hex
string (32 bytes). Anything else fails render-time with the generation
hint `openssl rand -hex 32`. For production, prefer `hmacKeySecret`
referencing a Kubernetes Secret so the key never lands in rendered
manifests or Helm release storage.

## Redaction provider composition

The recommended production topology is a real Presidio container in
the Collector pod. Both containers share a private `emptyDir` for the
Unix socket and run as UID/GID 65532 so the socket's `0600` permissions
are effective:

```yaml
fabric:
  redact:
    enabled: true
    unixSocket: /var/run/fabric/presidio.sock
    provider:
      mode: sidecar
      sidecar:
        tenantKeySecret:
          name: fabric-presidio-tenant-key
          key: tenant.key
```

For an operator-managed socket provider, use
`provider.mode=externalVolume` and supply a complete Kubernetes
`VolumeSource` under `provider.externalVolume.volumeSource`. The socket
must live beneath `provider.externalVolume.mountPath`. Whether a PVC,
CSI driver, or node-local volume supports Unix sockets is an operator
responsibility; the chart only accepts a concrete volume declaration.

The legacy `existingSocketProvider` string and
`acceptMissingProvider` escape hatch are not provider implementations
and no longer allow an enabled redaction pipeline to render.

## Policy bundle

An enabled policy always names an existing ConfigMap with one or more
`.rego` files:

```yaml
fabric:
  policy:
    enabled: true
    bundlePath: /etc/fabric/policy
    bundleConfigMap: approved-fabric-egress-policy
```

The chart never mounts an empty policy `emptyDir`. A missing path or
ConfigMap name fails at render time; a named ConfigMap that does not
exist prevents Kubernetes from starting the pod.

Profiles that set `exporter.requireEndpoint=true` also require an
`https://` endpoint and `exporter.insecure=false`. This binds the
positive, lockable regulated-export control to certificate-verified
TLS; an insecure override fails before any workload is rendered.

## Posture

- Distroless `nonroot` runtime (UID 65532).
- `readOnlyRootFilesystem: true`, drop all capabilities,
  `seccompProfile: RuntimeDefault`.
- ServiceAccount token **not** auto-mounted.
- No privileged paths in the default render.

## Health

The `health_check` extension listens on `:13133`. The liveness and
readiness probes hit `/` on that port. `helm test` spins a short-lived
curl pod that verifies the same endpoint end-to-end through the
Service.

## Known gaps

- A chart render can validate that a policy ConfigMap is named, but it
  cannot inspect an absent cluster object during an offline render.
  Admission/GitOps controls must ensure the approved ConfigMap exists.
- External redaction volumes are intentionally generic. Operators must
  qualify their volume implementation for Unix sockets and multi-pod
  access; the co-located sidecar is the supported default.
