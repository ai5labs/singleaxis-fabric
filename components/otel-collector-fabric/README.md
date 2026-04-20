# otel-collector-fabric

An OpenTelemetry Collector distribution that ships with SingleAxis
Fabric policy processors. Operators who prefer the Collector topology
over the in-process Bridge can plug Fabric's controls in at the same
place — the edge of the telemetry pipeline.

## What's in the box

- **`fabricguard` (logs processor)** — enforces the Fabric
  deny-by-default schema allowlist on agent decision logs. Mirrors the
  in-process Bridge stage, so policy stays identical whether an
  operator runs the Bridge or the Collector.
- **`fabricpolicy` (logs processor)** — gates log records through an
  OPA (Rego) policy bundle. Fail-closed on eval errors or non-boolean
  results. Mirrors the Bridge's OPA stage.
- **`fabricsampler` (logs processor)** — deterministic HMAC-keyed
  per-class sampler. Same records sample identically across retries
  and replicas, and across the Bridge/Collector topologies when they
  share a key.
- **`fabricredact` (logs processor)** — forwards every string
  attribute to the `fabric-presidio-sidecar` over a Unix socket and
  replaces hashed values in place. Fail-closed: any sidecar error
  drops the record. Mirrors the Bridge's Presidio redaction stage.
- **Standard upstream components** — `otlpreceiver`, `memorylimiter`,
  `batch`, `otlphttpexporter`, `debugexporter`.

## Build

Install the OpenTelemetry Collector Builder once:

```bash
go install go.opentelemetry.io/collector/cmd/builder@v0.150.0
```

Then, from this directory:

```bash
make test      # unit tests for the Fabric processors
make build     # runs ocb against ocb-config.yaml → dist/otelcol-fabric
```

The resulting `dist/otelcol-fabric` is a standalone binary.

## Run

```bash
./dist/otelcol-fabric --config examples/config.yaml
```

## Container image

A multi-stage `Dockerfile` ships alongside the source. The builder
stage runs OCB against `ocb-config.yaml` and produces a static binary;
the runtime stage ships that binary on `gcr.io/distroless/static` as
`nonroot`, exposing the standard OTLP ports.

```bash
# Build
docker build -t fabric-otelcol:local .

# Run — mount your collector config at /etc/otelcol-fabric/config.yaml
docker run --rm \
  -p 4317:4317 -p 4318:4318 -p 13133:13133 \
  -v /path/to/config.yaml:/etc/otelcol-fabric/config.yaml:ro \
  fabric-otelcol:local
```

Ports exposed: `4317` (OTLP/gRPC), `4318` (OTLP/HTTP), `13133`
(health_check extension — compiled in from contrib; wire it in your
collector config under `extensions.health_check` and list it in
`service.extensions`, as shown in `examples/config.yaml`).

If your pipeline uses `fabricredact`, also bind-mount the Presidio
sidecar's Unix socket into the container (e.g. `-v
/run/fabric:/run/fabric`) and point the processor's `unix_socket` at
that path in your config.

## Why a custom distro?

The Fabric schema allowlist and OPA policy need to run BEFORE data
leaves the operator's perimeter. Running them inside the Collector
lets operators apply Fabric compliance controls to any telemetry they
already route through otelcol, without adopting the full Bridge.

## License

Apache-2.0.
