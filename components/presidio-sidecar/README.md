# presidio-sidecar

Layer 1 Python sidecar that serves PII detection + deterministic
redaction over a Unix domain socket. Callers (the Fabric SDK's inline
redactor, the Telemetry Bridge's egress pipeline, or the OTel
Collector's redact processor) invoke it per string field; keeping it
in-pod keeps the call cheap and avoids egress hops.

## Authoritative spec

[`../../specs/005-guardrails-inline.md`](../../specs/005-guardrails-inline.md)
— inline guardrails; the sidecar's role as an egress redactor is
described in [`../../specs/004-telemetry-bridge.md`](../../specs/004-telemetry-bridge.md).

## Why a separate process

Presidio and its spaCy models are Python-native. Rather than embed a
Python interpreter in the Go binary (cgo + shipping spaCy models
complicates the container), we run Presidio as a sidecar in the same
pod. The Go pipeline speaks to it over a UDS.

## API shape

Single endpoint, CBOR or JSON:

```
POST /v1/redact
{
  "path":  "decision_summary.rubric_id",
  "value": "some-string"
}
-->
{
  "value":        "some-string OR HMAC(value, tenant_key)",
  "hashed":       true | false,
  "pii_category": "EMAIL_ADDRESS" | ""
}
```

The sidecar never logs the request `value`. Logs contain the path,
the hashed flag, and the PII category only.

## Status

Pre-alpha — scaffold only. The sidecar today builds a bare
`presidio_analyzer.AnalyzerEngine()` (spaCy defaults, no tenant
overrides). A `PassthroughAnalyzer` is used when `presidio-analyzer`
is not installed so tests and local dev stay light.

### Running the sidecar

The sidecar refuses to start without a tenant HMAC key. Supply one
via `--tenant-key-file`:

```bash
fabric-presidio-sidecar --port 8787 --tenant-key-file /etc/fabric/tenant.key
```

The file must contain a non-empty byte string that is **not** the
literal `change-me` (the historical default sentinel that previously
made HMACs reversible across deployments).

### Future work (Phase 2)

- **Tenant-overridable analyzer registry.** A `config/` directory with
  per-tenant recognizer YAML, overlayable from Helm values. Not
  shipped yet — the current build wires a single process-wide
  `AnalyzerEngine()`. Track via the roadmap; do not rely on a
  `config/` directory existing today.
- **Custom recognizers.** Per-tenant allowlists of token patterns
  that should never be flagged as PII (e.g. internal IDs). Also
  Phase 2.
