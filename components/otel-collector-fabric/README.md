# Fabric OpenTelemetry Collector distribution

This OpenTelemetry Collector Builder (OCB) distribution applies Fabric's
telemetry controls before logs and traces leave an operator-controlled
environment. It can export to any OTLP/HTTP backend; the SingleAxis
Platform is an optional destination, not a runtime dependency.

## Included components

- `fabricguard` — deny-by-default schema allowlisting for logs and traces.
- `fabricpolicy` — fail-closed OPA/Rego egress policy evaluation.
- `fabricredact` — fail-closed telemetry redaction over a pod-local Unix
  socket to the Fabric Presidio provider.
- `fabricsampler` — deterministic HMAC-keyed per-class sampling.
- Upstream OTLP receiver, memory limiter, batch processor, OTLP/HTTP and
  debug exporters, health/zPages extensions, and the file-storage extension
  used by durable exporter queues.

## Build and test

Install the version-pinned OCB tool, then build from this directory:

```bash
go install go.opentelemetry.io/collector/cmd/builder@v0.150.0
make test
make build
```

The result is `dist/otelcol-fabric`. The OCB manifest pins every upstream
component to v0.150.0 and resolves the four local Fabric processors from
their source directories.

## Distribution configuration acceptance

After building, qualify the actual binary rather than only rendering Helm
text:

```bash
make qualify-config
```

For the locally built container image used by CI:

```bash
make qualify-image COLLECTOR_IMAGE=fabric-otelcol:pr
```

The test generates a temporary CA, server certificate, HMAC key, queue
directory, and Collector configuration. It then proves that the built
artifact starts with:

- mTLS on both OTLP/gRPC and OTLP/HTTP receivers (`cert_file`, `key_file`,
  and `client_ca_file`);
- an `otlp_http/fabric` exporter using the file-storage persistent queue;
- `fabricguard`, `fabricredact`, `fabricpolicy`, and `fabricsampler` in both
  logs and traces pipelines.

The image-mode test uses `--network none`, and the binary-mode exporter is
an unused loopback endpoint. No telemetry is emitted. This qualifies
component and configuration compatibility only; it does not qualify live
delivery, backend authentication, redaction behavior, or durability under
failure. Temporary key material is removed on exit.

## Run

The example configuration demonstrates TLS/authenticated OTLP export with a
disk-backed sending queue. Supply its referenced policy, key, certificate,
credential, and writable queue paths before starting it:

```bash
export FABRIC_EXPORT_ENDPOINT=https://otlp.example.com
export FABRIC_EXPORT_AUTH='Bearer <token>'
./dist/otelcol-fabric --config examples/config.yaml
```

For a container deployment, mount configuration and queue storage separately:

```bash
docker build -t fabric-otelcol:local .
docker run --rm \
  -p 4317:4317 -p 4318:4318 -p 13133:13133 \
  -e FABRIC_EXPORT_ENDPOINT -e FABRIC_EXPORT_AUTH \
  -v /path/to/config.yaml:/etc/otelcol-fabric/config.yaml:ro \
  -v fabric-otel-queue:/var/lib/otelcol-fabric/storage \
  fabric-otelcol:local
```

If `fabricredact` is configured, the Presidio Unix socket must also be
mounted into the same container/pod filesystem. The Helm chart renders the
supported pod-local sidecar topology.

## Reliability and security boundary

Compiling `filestorageextension` does not by itself make export durable.
The exporter must set `sending_queue.storage`, the extension must be enabled
under `service.extensions`, and its directory must be a persistent writable
volume. The Helm chart owns this wiring and validates regulated combinations.

Persistent queues provide bounded at-least-once transport. They may replay a
batch when acknowledgement state is ambiguous, and they can still lose data
on permanent errors, queue/disk exhaustion, storage loss, or pre-queue policy
drops. The receiving system must deduplicate using stable Fabric identifiers
and provide the authoritative evidence retention layer.

Outbound credentials belong in environment/secret sources, never inline in
Collector YAML. TLS verification should remain enabled; mount private CA and
mTLS material read-only where required. Queue volumes can contain sensitive
telemetry and therefore need storage-layer encryption, least-privilege access,
backup, and lifecycle controls supplied by the operator.

## License

Apache-2.0.
