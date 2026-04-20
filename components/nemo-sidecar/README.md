# nemo-sidecar

Python sidecar that runs the NeMo Guardrails Colang runner and serves
the `POST /v1/check` endpoint the Fabric SDK's `UDSNemoClient` talks
to over a Unix domain socket.

## Authoritative spec

[`../../specs/005-guardrails-inline.md`](../../specs/005-guardrails-inline.md)

## Why a separate process

`nemoguardrails` pulls `transformers`, an LLM provider, and a handful
of model dependencies. We don't want those in the SDK wheel — agents
that import `fabric` must stay stdlib-heavy. Running NeMo as an
in-pod sidecar over UDS keeps the SDK thin without forcing a REST
hop.

## API shape

Single endpoint, JSON:

```
POST /v1/check
{
  "phase": "input" | "output_stream" | "output_final",
  "path":  "input" | "output_chunk" | "output_final",
  "value": "<text>"
}
-->
{
  "allowed":        true | false,
  "action":         "allow" | "redact" | "block" | "warn",
  "rail":           "<rail-id>",
  "block_response": "<canned refusal>" | null,
  "modified_value": "<possibly-rewritten text>"
}
```

`action == "block"` is the authoritative blocking signal for the SDK;
every non-allow action implies a rail fired. `rail` is surfaced on
the Fabric OTel span event as `nemo:<rail>`.

## Status

Pre-alpha — scaffold only. The Colang rails config lives in
`config/` (tenant-overridable via Helm) and is loaded with
`--rails-config`. Without `--rails-config` the sidecar serves a
passthrough engine — useful for local development but **not** a
production posture; the host should refuse to start a tenant that
wired the sidecar but left the config empty.

## Run locally

```bash
pip install -e '.[dev]'            # passthrough engine only
fabric-nemo-sidecar --port 8787
curl -sS localhost:8787/healthz

pip install -e '.[dev,nemo]'       # with real NeMo Colang runner
fabric-nemo-sidecar --uds /tmp/nemo.sock --rails-config ./config
```

## Concurrency and timeouts

The sidecar wraps `LLMRails.generate()` in a dedicated thread pool
with a per-request internal timeout, so a slow rails engine cannot
starve `/healthz` by pinning uvicorn's default threadpool. All knobs
are environment variables; defaults are safe for production:

| Env var | Default | Meaning |
| --- | --- | --- |
| `FABRIC_LIMIT_CONCURRENCY` | `16` | Max in-flight requests (uvicorn `limit_concurrency` and the `/check` thread pool size). |
| `FABRIC_REQUEST_TIMEOUT_MS` | `800` | Per-request wallclock budget around `LLMRails.generate()`. Exceeding it returns `504`; the SDK treats 504 as fail-closed `block`. |

`timeout_keep_alive` is pinned to `5s` to shed idle clients quickly.
