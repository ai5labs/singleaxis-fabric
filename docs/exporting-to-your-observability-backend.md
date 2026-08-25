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
  --set otel-collector.exporter.endpoint=https://otlp.example.com
```

The rule the chart enforces is that **a rendered pipeline never points
at an endpoint that is not set**:

- `endpoint` **set** — the `otlp_http/fabric` exporter is rendered and
  used. `debug` is added alongside it when `debugExporter.enabled=true`.
- `endpoint` **empty** — `otlp_http/fabric` is not rendered at all. The
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

Create an operator-owned Secret whose `authorization` key contains only the
Datadog API key, then map that value to the required outbound header:

```bash
kubectl -n fabric-system create secret generic fabric-datadog-otlp-auth \
  --from-file=authorization=/secure/path/datadog-api-key
helm install fabric ./charts/fabric \
  --set otel-collector.exporter.endpoint=https://otlp.datadoghq.com:443 \
  --set otel-collector.exporter.insecure=false \
  --set otel-collector.exporter.auth.headerName=dd-api-key \
  --set otel-collector.exporter.auth.secret.name=fabric-datadog-otlp-auth
```

(Replace `datadoghq.com` with your region domain.)

## Honeycomb

```bash
kubectl -n fabric-system create secret generic fabric-honeycomb-otlp-auth \
  --from-file=authorization=/secure/path/honeycomb-api-key
helm install fabric ./charts/fabric \
  --set otel-collector.exporter.endpoint=https://api.honeycomb.io:443 \
  --set otel-collector.exporter.insecure=false \
  --set otel-collector.exporter.auth.headerName=x-honeycomb-team \
  --set otel-collector.exporter.auth.secret.name=fabric-honeycomb-otlp-auth
```

## Grafana Tempo / Cloud (via OTLP gateway)

```bash
helm install fabric ./charts/fabric \
  --set otel-collector.exporter.endpoint=https://otlp-gateway-prod-REGION.grafana.net:443 \
  --set otel-collector.exporter.insecure=false
```

Add Basic auth headers per Grafana Cloud's OTLP configuration page.
Store the complete `Authorization` header value in an operator-created Secret
and set `otel-collector.exporter.auth.secret.name`; do not put it in Helm
values.

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

The chart deliberately exposes one qualified OTLP/HTTP destination. Do not
hand-edit its generated ConfigMap: Helm will reconcile over the change and the
result is outside the tested delivery contract. To fan out, send Fabric to a
customer-owned Collector or gateway and configure multiple exporters there.
That also centralizes vendor credentials, retry policy, and delivery alerts.

## NetworkPolicy considerations

The `eu-ai-act-high-risk` profile enables `denyDefault: true` and refuses to
render until the operator supplies an explicit exporter peer and port. There
is no invented in-namespace route:

```yaml
otel-collector:
  networkPolicy:
    exporterEgress:
      requireExplicit: true
      to:
        - namespaceSelector:
          matchLabels:
            kubernetes.io/metadata.name: approved-egress
        - ipBlock:
            cidr: 203.0.113.10/32
      ports:
        - protocol: TCP
          port: 443
```

`203.0.113.10/32` is documentation-only. Kubernetes NetworkPolicy does not
support DNS names and cannot prove that a CIDR belongs to the configured URL.
For dynamic SaaS endpoints, route through a controlled egress gateway with a
stable, approved peer. NetworkPolicy enforcement also depends on the cluster
CNI.

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

For a development profile with plaintext receiver ingress, the simplest
transport smoke is:

```bash
kubectl -n fabric-system port-forward svc/fabric-otel-collector 4318:4318 &
curl -s -o /dev/null -w "%{http_code}\n" \
  -X POST http://localhost:4318/v1/traces \
  -H "Content-Type: application/json" \
  -d '{"resourceSpans": []}'
# expect 200
```

The high-risk profile rejects that plaintext request. Verify it with an OTLP
client presenting a certificate signed by `fabric-otel-client-ca` and validate
the server name against `fabric-otel-receiver-tls`. A successful receiver call
only proves ingress; confirm exporter queue/send metrics and the destination
record before declaring end-to-end delivery healthy.

Then run the reference agent (or your own instrumented agent) and
check the backend's UI for the `fabric.decision` span.

## See also

- [`charts/fabric/charts/otel-collector/values.yaml`](../charts/fabric/charts/otel-collector/values.yaml)
  for the full exporter config surface.
- [`docs/quickstart.md`](quickstart.md) for the SDK-side wire-up.
- [`docs/architecture.md`](architecture.md) for what the collector
  actually does to spans before egress.
