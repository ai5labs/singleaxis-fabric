# Exporting to your observability backend

Fabric is OTLP-native. Once the SDK and collector are running, every
agent decision produces an OpenTelemetry span (with `gen_ai.*`
semantic conventions on LLM calls from v0.2.0 onward) that can land
in any backend that speaks OTLP/HTTP.

This page shows the wire-up for the most common backends. The
collector's exporter endpoint is the single setting that determines
where spans actually go.

## Where the setting lives

`charts/fabric/charts/otel-collector/values.yaml`:

```yaml
exporter:
  endpoint: ""        # no default; empty falls back to pod stdout
  insecure: true      # set false for TLS-fronted backends
```

Override at install via `helm install` or a profile YAML:

```bash
helm install fabric ./charts/fabric \
  --set otel-collector.exporter.endpoint=<URL>
```

The rule the chart enforces is that **a rendered pipeline never points
at an endpoint that is not set**:

- `endpoint` **set** — the `otlphttp/fabric` exporter is rendered and
  used. `debug` is added alongside it when `debugExporter.enabled=true`.
- `endpoint` **empty** — `otlphttp/fabric` is not rendered at all. The
  pipelines fall back to `debug`, so spans land in the collector pod's
  stdout (`kubectl logs`) instead of vanishing, and `NOTES.txt` prints
  a loud post-install warning. This is a **dev posture**: visible, but
  not durable and not an audit trail.

There is no longer an `acceptUnsetEndpoint` escape hatch — an unset
endpoint is a supported, loudly announced state rather than a
render-time error. If you were passing that flag, drop it.

## Bundled Langfuse (opt-in)

The Helm chart ships a Langfuse subchart. It is **opt-in** and off in
every shipped profile (`langfuse.enabled` defaults to `false`).

It does **not** bundle a database. The subchart deploys Langfuse only;
you supply an external Postgres, either inline via `database.url` or by
reference via `database.dsnSecret.name`. Without one the chart fails at
render with `langfuse: set database.url or database.dsnSecret.name` —
it will not deploy a broken instance.

```bash
helm install fabric ./charts/fabric \
  --set langfuse.enabled=true \
  --set langfuse.database.dsnSecret.name=fabric-langfuse-db \
  --set langfuse.bootstrap.enabled=true
```

The Service is named after the release, so with `helm install fabric`
it resolves at `http://fabric-langfuse:3000` — not `http://langfuse:3000`.

The `langfuse-bootstrap` Job configures the Langfuse instance with
Fabric's curated score configs, prompt presets, and saved-view URLs
(idempotent — rerun safe). It is Fabric-built tooling, so its image is
published at the **Fabric release version** (not Langfuse's upstream
appVersion) and the chart tags it accordingly by default.

> **Do not point the collector's OTLP exporter at the bundled
> Langfuse yet.** The subchart pins Langfuse v2 (`appVersion 2.93.0`),
> which is a passive sink written to over its own ingestion API — we
> have not verified that it accepts OTLP/HTTP at `/v1/traces`. Until
> that is confirmed, treat the bundled Langfuse as a UI you populate
> by other means, and send collector traffic to one of the backends
> below.

## Arize Phoenix

```bash
helm install fabric ./charts/fabric \
  --set otel-collector.exporter.endpoint=http://phoenix:6006/v1/traces
```

Phoenix's "LLM" view keys off `gen_ai.*` attributes, which Fabric
emits from v0.2.0. Earlier versions appear as generic spans —
upgrade to v0.2.x for full LLM dashboard coverage.

## Datadog (OTLP intake)

```bash
helm install fabric ./charts/fabric \
  --set otel-collector.exporter.endpoint=https://otlp.datadoghq.com:443 \
  --set otel-collector.exporter.insecure=false \
  --set-string otel-collector.exporter.headers.dd-api-key=$DD_API_KEY
```

(Replace `datadoghq.com` with your region domain.)

## Honeycomb

```bash
helm install fabric ./charts/fabric \
  --set otel-collector.exporter.endpoint=https://api.honeycomb.io:443 \
  --set otel-collector.exporter.insecure=false \
  --set-string otel-collector.exporter.headers.x-honeycomb-team=$HONEYCOMB_API_KEY
```

## Grafana Tempo / Cloud (via OTLP gateway)

```bash
helm install fabric ./charts/fabric \
  --set otel-collector.exporter.endpoint=https://otlp-gateway-prod-<region>.grafana.net:443 \
  --set otel-collector.exporter.insecure=false
```

Add Basic auth headers per Grafana Cloud's OTLP configuration page.

## Your own collector chain

Operators running their own OTel collector (e.g., as part of an
existing observability platform) point Fabric at it:

```bash
helm install fabric ./charts/fabric \
  --set otel-collector.exporter.endpoint=http://my-otel-collector.observability.svc:4318
```

From there, the operator's collector applies the rest of their
processor chain (sampling, retention routing, vendor-specific
exporters).

## Multiple destinations (fan-out)

The chart ships a single OTLP/HTTP exporter by default. To fan out
to multiple backends, edit the collector's pipeline config — the
`fabricredact`, `fabricguard`, `fabricsampler` chain is independent
of the exporter list, so adding additional exporters does not change
the privacy/policy enforcement applied to spans.

For most operators, the simpler pattern is: send to one OTLP
endpoint (your own collector), and let that collector fan out.

## NetworkPolicy considerations

The `eu-ai-act-high-risk` profile enables `denyDefault: true` plus
per-subchart NetworkPolicies. The collector's `egressTo` defaults to
the `fabric-system` namespace only, so external destinations
(Datadog, Honeycomb, anywhere outside the cluster) require operator
overrides:

```yaml
otel-collector:
  networkPolicy:
    egressTo:
      - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: fabric-system
      - ipBlock:
          cidr: 0.0.0.0/0   # external — tighten as needed
        ports:
          - protocol: TCP
            port: 443
```

For tighter setups, replace `0.0.0.0/0` with the egress
NAT/proxy CIDR your cluster uses.

## What's in the span

Until v0.2.0, Fabric emits one `fabric.decision` span per agent turn
with identity tags (`fabric.tenant_id`, `fabric.agent_id`, `…`),
guardrail/escalation/retrieval/memory events, and any custom
attributes the application attaches via `decision.set_attribute`.

From v0.2.0, `Decision.llm_call` and `Decision.tool_call` add child
spans with `gen_ai.*` standard attributes (model, tokens, finish
reason, tool name, etc.). Auto-instrument extras (`pip install
"singleaxis-fabric[openai]"`, etc.) wire the upstream
`opentelemetry-instrumentation-*` packages so LLM SDK calls light up
without manual wrapping.

## Verifying the wire

After install, the simplest verification:

```bash
kubectl -n fabric-system port-forward svc/fabric-otel-collector 4318:4318 &
curl -s -o /dev/null -w "%{http_code}\n" \
  -X POST http://localhost:4318/v1/traces \
  -H "Content-Type: application/json" \
  -d '{"resourceSpans": []}'
# expect 200
```

Then run the reference agent (or your own instrumented agent) and
check the backend's UI for the `fabric.decision` span.

## See also

- [`charts/fabric/charts/otel-collector/values.yaml`](../charts/fabric/charts/otel-collector/values.yaml)
  for the full exporter config surface.
- [`docs/quickstart.md`](quickstart.md) for the SDK-side wire-up.
- [`docs/architecture.md`](architecture.md) for what the collector
  actually does to spans before egress.
