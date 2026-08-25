# Fabric Relay

Fabric Relay is a narrow OpenTelemetry delivery tier for exporting approved
agent activity from a customer-controlled environment. It receives OTLP,
buffers outbound telemetry on local durable storage, retries transient delivery
failures, and exposes health and self-observability endpoints.

Production deployments terminate mutually authenticated TLS on both OTLP/gRPC
and OTLP/HTTP. Server keys and the trusted client CA are mounted from the
customer's secret store; they are not embedded in Collector configuration or
Helm values.

It deliberately does **not** perform capture-time policy, PII treatment, or
schema normalization. Those controls belong upstream in the Fabric Collector.
Keeping the relay separate makes the egress boundary small and independently
operable.

## Delivery semantics

Production configuration uses the Collector exporter helper's persistent
`file_storage` sending queue with `block_on_overflow: true` and an unlimited
retry window. Once the Collector has accepted an item into that queue, it will
retry until the configured destination acknowledges it. This is an
**at-least-once delivery** contract: destinations must tolerate duplicates.

It is not a claim of lossless storage. Data can still be lost through storage
corruption, exhausted or forcibly removed volumes, operator deletion, or a
destination that acknowledges data before durably recording it. Capacity,
backup, recovery and destination acknowledgement behavior must be qualified for
each production deployment.

## Build and verify

```bash
make test
make build
make validate-example
```

The static test runs without network access. `validate-example` uses the built
Collector binary and is the authoritative component configuration check.

The Kubernetes installation is documented in
`charts/fabric/charts/fabric-relay/README.md`. Production runtime persistence
has not been proven by template tests; that chart includes a reproducible
destination-outage test procedure for cluster qualification.
