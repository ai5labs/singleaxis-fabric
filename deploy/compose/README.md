# Fabric Node evaluation harness

This is a small, explicitly non-production setup for trying the Fabric OSS
recorder:

```text
OTLP -> Fabric Node -> default-deny allowlist -> durable queue -> test sink
```

It contains two runtime services:

- `fabric-node`: the current Collector-based recorder implementation;
- `test-sink`: a controlled OTLP/HTTP destination that stores requests on a
  durable Docker volume and returns success only after fsync.

A short-lived `queue-init` helper gives the non-root Fabric Node ownership of
its Docker queue volume and exits before the recorder starts. It is not a
runtime hop and has no network dependency.

There are no guardrails, prompt-time PII sidecars, judges, red-team runners,
management services, or observability UI.

## Start and inspect

```bash
cd deploy/compose
make up
make status
make smoke
```

Send OTLP/HTTP to `http://localhost:4318`. The controlled sink exposes:

- `GET http://localhost:8080/health`
- `GET http://localhost:8080/count`

Run the restart/outage qualification:

```bash
make qualify
```

The fixture simulates a passive shadow of an agent performing a model call,
retrieval, tool call, non-committed side effect and checkpoint. The deterministic
CI critic evaluates structured observations from the same scenario; it is test
tooling, not an LLM judge or runtime assurance component.

The qualification proves that this configuration:

1. accepts a known trace;
2. strips a non-allowlisted marker before export;
3. preserves allowlisted reconstruction metadata;
4. queues a trace while the destination is unavailable;
5. survives a Fabric Node restart;
6. delivers the queued request after the sink returns.

## Honest limitations

This harness is plaintext and unauthenticated. It is for local evaluation only.
Use the Helm `shadow-production` profile across a trust boundary.

The test sink's `200` response has a deliberately strong, test-specific
meaning: that request was fsynced to its Docker volume. Fabric cannot infer the
same meaning from an arbitrary OTLP destination. In production, distinguish
Collector acceptance, local queueing, destination acknowledgement, and
destination durable persistence.

The queue is at least once, not exactly once. Duplicate delivery remains
possible after ambiguous acknowledgements, and the queue is not an
authoritative evidence store.

## Remove evaluation data

```bash
make down        # keeps queue and sink volumes
make reset       # deletes this Compose project's evaluation volumes
```
