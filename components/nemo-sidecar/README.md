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
  "modified_value": "<the submitted value, rewritten only when action == redact>"
}
```

`action == "block"` is the authoritative blocking signal for the SDK;
every non-allow action implies a rail fired. `rail` is surfaced on
the Fabric OTel span event as `nemo:<rail>`.

**`modified_value` contract.** It is a transformation of the submitted
value and MUST equal that value byte-for-byte unless
`action == "redact"`. **An assistant completion is not a modified
value.** A refusal belongs in `block_response`, never in
`modified_value`.

This is normative because violating it is silent and dangerous. NeMo's
Colang path runs `LLMRails.generate()`, a chat completion — returning
that text as the "modified" input replaced the caller's message with
chatbot output under `allowed: true`, with no error. `RailsChecker`
enforces the rule for every engine, including operator-supplied ones,
and logs a warning when an engine violates it.

Callers must honour `allowed` and must never treat `modified_value` as
a safe substitute for the input on a blocked turn.

## Runtime modes

The sidecar refuses to start without a rails configuration unless
`--allow-passthrough` is explicitly supplied. The public Helm chart
selects `--starter-literal-only` by default. That engine validates the
canonical model-free declaration, blocks known literal
instruction-override phrases, and allows benign input without
initializing NeMo or making external calls. It is a safe, testable
baseline, not a semantic or production-grade jailbreak detector.

Custom Colang configurations are loaded with `--rails-config`. Add
`--enable-default-literal-filter` to retain the deterministic first
line before model-backed rails. The chart does this by default.

## Run locally

```bash
pip install -e '.[dev]'            # passthrough engine only
fabric-nemo-sidecar --port 8787 --allow-passthrough
curl -sS localhost:8787/healthz

pip install -e '.[dev,nemo]'       # with real NeMo Colang runner
fabric-nemo-sidecar --uds /tmp/nemo.sock \
  --rails-config ./rails/starter \
  --starter-literal-only \
  --enable-default-literal-filter
```

## Concurrency and timeouts

The sidecar wraps engine checks in a dedicated thread pool
with a per-request internal timeout, so a slow rails engine cannot
starve `/healthz` by pinning uvicorn's default threadpool. All knobs
are environment variables. Operators must qualify them for their own
rails, topology, and workload:

| Env var | Default | Meaning |
| --- | --- | --- |
| `FABRIC_LIMIT_CONCURRENCY` | `16` | Max in-flight requests (uvicorn `limit_concurrency` and the `/check` thread pool size). |
| `FABRIC_REQUEST_TIMEOUT_MS` | `800` | Per-request wallclock budget around the configured engine. Exceeding it returns `504`; the SDK treats 504 as fail-closed `block`. |

`timeout_keep_alive` is pinned to `5s` to shed idle clients quickly.
