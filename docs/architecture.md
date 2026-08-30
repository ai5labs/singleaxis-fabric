# Architecture

The recorder-v1 architecture has one pipeline and one deployable runtime:

```text
                                  customer trust boundary
                                      |
SDK / adapter / existing OTLP ------+--> Fabric Node --> approved destination
                                         |    |    |
                                      capture protect deliver
```

The accepted design of record is
[spec 027](../specs/027-recorder-v1.md).

## Components

### Fabric SDKs

SDKs are optional instrumentation libraries. Use them when the customer can
modify agent code and needs richer model, tool, retrieval, memory, delegation,
side-effect, retry, and failure correlation. They export OpenTelemetry and do
not require a SingleAxis backend.

An existing system does not need to adopt the SDK. Its current OpenTelemetry
pipeline can send compatible telemetry directly to Fabric Node.

### Fabric Node

Fabric Node is the existing Fabric OpenTelemetry Collector distribution. One
process performs the complete recorder path:

1. **Capture** — authenticate and receive OTLP, categorize supported metadata,
   and retain
   causal fields supplied by the source.
2. **Protect** — apply an exact metadata allowlist and remove unapproved
   content before export.
3. **Deliver** — place protected records in a persistent sending queue and
   retry export to the configured OTLP/HTTP destination.

There is no required Collector-to-Relay hop in recorder v1. The older Relay
source is experimental and is not published or installed by this release.

### `fabricctl`

`fabricctl init` creates a local recorder configuration and a deterministic
receipt. The result is explicitly marked `not-installed`; configuration does
not mutate a cluster. Validation and digest operations work offline.

### Public contracts

The contracts separate interoperability from proprietary services:

- Activity Envelope v2 describes the downstream normalization target for
  reconstructable activity and causal identity. Fabric Node transports
  protected OTLP; it does not materialize the JSON envelope on its wire path.
- Connector manifests disclose coverage, identity, ordering, content access,
  and blind spots.
- Privacy assertions bind a processor's claim to the input, protected output,
  policy, and build; unsigned assertions remain explicitly unverified.
- Delivery batches and receipts distinguish queueing, transmission,
  destination acceptance, and durable-persistence evidence.

## Trust boundaries

Raw content is disabled by default. Protection runs before the export boundary,
not after telemetry reaches SingleAxis. The production profile requires mTLS
ingress, authenticated HTTPS egress, persistent storage, and explicit network
policy peers.

The destination can be customer-owned, a private SingleAxis deployment, or
SingleAxis Platform. Fabric Node behaves the same in each model.

## Passive monitoring

Recorder v1 is outside the monitored agent's decision path. A collector outage
must not change the agent's business decision. Persistent queues and retry
handle destination outages independently.

Fabric may observe only what the chosen integration exposes. SDK
instrumentation is richer than generic OTLP, and a gateway sees only traffic
that traverses it. Connector capability manifests must disclose those blind
spots rather than claiming universal capture.

## What happens after delivery

SingleAxis Platform or a customer backend can monitor, evaluate, investigate,
and build a Decision Graph from the open record. Those services are downstream
consumers, not hidden dependencies of Fabric Node.

Prompt-time PII blocking, guardrails, policy authorization, and tool blocking
are enforcement. They require deliberate placement in the request or execution
path and are not enabled by the passive recorder.
