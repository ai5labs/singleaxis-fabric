<div align="center">

# SingleAxis Fabric

**Open-source operational telemetry and control substrate for autonomous systems.**

Decision-span tracing, causal execution telemetry, fail-loud guardrails,
and human-in-the-loop primitives — OpenTelemetry-native, with adapters
for LangGraph, Microsoft Agent Framework, and CrewAI. Hardened defaults
for regulated environments: the `eu-ai-act-high-risk` Helm profile ships
today; Decision Graph, replay orchestration, and full audit-trail
evidence generation land with the SingleAxis commercial control plane.

[![PyPI](https://img.shields.io/pypi/v/singleaxis-fabric.svg)](https://pypi.org/project/singleaxis-fabric/)
[![Python](https://img.shields.io/pypi/pyversions/singleaxis-fabric.svg)](https://pypi.org/project/singleaxis-fabric/)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![CI](https://github.com/singleaxis/singleaxis-fabric/actions/workflows/ci.yml/badge.svg)](https://github.com/singleaxis/singleaxis-fabric/actions/workflows/ci.yml)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/singleaxis/singleaxis-fabric/badge)](https://securityscorecards.dev/viewer/?uri=github.com/singleaxis/singleaxis-fabric)

[Quickstart](docs/quickstart.md) ·
[Product model](specs/025-product-planes-and-packaging.md) ·
[Architecture](docs/architecture.md) ·
[GenAI conventions](docs/genai-semantic-conventions.md) ·
[Deployment](docs/deployment.md) ·
[API stability](docs/api-stability.md) ·
[Reference agent](examples/reference-agent/) ·
[Specs](specs/) ·
[Download the SASF white paper v2.3 (PDF)](https://github.com/singleaxis/sasf/releases/download/v2.3/SASF_White_Paper_v2_3.pdf)

</div>

---

## Why Fabric

Teams shipping LLM agents into regulated environments — banks, hospitals,
insurers, public sector — keep building the same five things in-house:

1. A way to record what the agent **decided** and **why**, so auditors and
   incident responders can reconstruct a turn months later.
2. Inline **PII redaction** and **policy rails** that fail loud instead of
   silently leaking or complying.
3. A **human-in-the-loop** primitive that pauses an agent turn, routes it
   to a reviewer, and resumes with a signed verdict.
4. **Retrieval provenance** — which documents were pulled, what was
   hashed, what the agent saw vs. what it ignored.
5. A deployment shape that doesn't make the agent request path wait on
   any of it.

Fabric ships the substrate for all five as a drop-in library, sidecars,
and a Helm chart. Apache-2.0. Zero-signup. Works offline. The one
partial is (3): the OSS distribution gives you the pause-and-emit
primitive and the adapter wiring, not the reviewer queue or the
signed-verdict resume — those are commercial (see
[`specs/007-escalation-workflow.md`](specs/007-escalation-workflow.md)).

## What you get

- **Decision spans** — one OpenTelemetry span per agent turn, tagged
  with tenant / agent / session / request / user, plus span events for
  every retrieval, guardrail check, memory write, and escalation.
- **Every interaction captured** — MCP servers (with tool-definition
  drift detection), skills, sub-agents, hooks, file access, and a
  universal `record_interaction` for *any* surface — all hash-on-span,
  with generic baseline / taxonomy-tag (MITRE ATLAS, OWASP LLM) /
  signature-verification helpers. See
  [`docs/capturing-interactions.md`](docs/capturing-interactions.md).
- **Inline guardrails** — [Presidio](https://microsoft.github.io/presidio/)
  for PII redaction and [NeMo Guardrails](https://github.com/NVIDIA/NeMo-Guardrails)
  for Colang policy rails, both exposed over Unix domain sockets
  (sub-millisecond transport, no TCP hop).
- **Escalation primitive** — `decision.request_escalation(...)` returns a
  framework-agnostic payload you hand to whatever HITL mechanism your
  orchestrator exposes (LangGraph `interrupt()`, MAF `request_info`,
  CrewAI `human_feedback`, or your own queue).
- **Retrieval + memory recording** — SHA-256-hashed locally (raw text
  never leaves the span), allowlisted attributes, rolling counters on
  the decision span. Maps cleanly onto a provenance graph.
- **OTel Collector distribution** — preconfigured with the Fabric
  processor chain (attribute allowlisting, tenant scoping, redaction,
  policy hooks, plus HMAC tail sampling that ships off so a default
  install keeps every event). Fans out to Tempo, Jaeger, Honeycomb,
  Datadog — anything that speaks OTLP/HTTP. Installs by default.
- **Helm chart with regulatory profiles** — `permissive-dev` for
  evaluation and `eu-ai-act-high-risk` for production under the EU AI
  Act ship today. NIST AI RMF, ISO/IEC 42001, SR 11-7, and HIPAA
  profiles are roadmap (see [`specs/008-deployment-model.md`](specs/008-deployment-model.md)).
- **First-class adapters** — [LangGraph](https://langchain-ai.github.io/langgraph/),
  [Microsoft Agent Framework](https://learn.microsoft.com/en-us/agent-framework/),
  and [CrewAI](https://www.crewai.com/). Installed via extras; core
  stays framework-neutral.
- **Sync and async** — `Decision`, `LLMCall`, and `ToolCall` work as
  both `with` and `async with`; blocking guardrail / policy / judge I/O
  has non-blocking `a`-prefixed variants that offload off the event
  loop. The emitted span is identical either way.

### One principle makes all of this practical

> **The agent request path never blocks on a Fabric HTTP call.**

SDK work is in-process (target `<1ms` P99). Guardrail sidecars run
over a Unix domain socket (target `<100ms` P99 budget per check).
Everything else — judges, escalation bookkeeping, provenance writes,
evidence generation — happens asynchronously off the OTel stream.
Security tooling that blocks request paths gets ripped out; Fabric
stays in the path only where latency budgets justify it. Numbers
above are design budgets enforced by component readiness probes, not
measured P99 guarantees. An opt-in micro-benchmark suite
(`sdk/python/benchmarks/`) reports the SDK's per-decision overhead;
its numbers are informational and machine-dependent.

## Install

```bash
pip install singleaxis-fabric                        # core SDK
pip install "singleaxis-fabric[otlp]"                # + OTLP/HTTP exporter
pip install "singleaxis-fabric[langgraph]"           # + LangGraph adapter
pip install "singleaxis-fabric[agent-framework]"     # + Microsoft Agent Framework
pip install "singleaxis-fabric[crewai]"              # + CrewAI adapter
```

Python 3.11+ (the rest of the repo targets 3.12).

## 60-second example

```python
# Requires the [otlp] extra for OTLPSpanExporter:
#   pip install "singleaxis-fabric[otlp]"
import os
from fabric import Fabric, FabricConfig, install_default_provider
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter

# One-time: point the SDK at your OTel Collector (or any OTLP sink).
# Raises with a clear message if the env var is unset — set it before
# running (e.g. export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318).
endpoint = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT")
if not endpoint:
    raise SystemExit(
        "Set OTEL_EXPORTER_OTLP_ENDPOINT to your OTel Collector's OTLP/HTTP "
        "endpoint (e.g. http://localhost:4318) before running this example."
    )
install_default_provider(
    service_name="support-bot",
    exporter=OTLPSpanExporter(endpoint=endpoint),
)

# Tenant + agent identity are required. Either pass them explicitly:
fabric = Fabric(FabricConfig(tenant_id="acme-prod", agent_id="support-bot"))
# ...or set FABRIC_TENANT_ID and FABRIC_AGENT_ID and call Fabric.from_env().

with fabric.decision(
    session_id="sess-1",
    request_id="req-1",
    user_id="user-42",
) as decision:
    safe_input = decision.guard_input("hello")               # Presidio rail

    # Wrap the LLM call in a child span so the trace tree captures
    # gen_ai.* semantic conventions (model, token counts, finish
    # reason) — Phoenix's LLM view, Langfuse cost dashboards, and
    # any backend keyed on either namespace render natively.
    with decision.llm_call(provider="anthropic", model="claude-opus-4-8") as call:
        answer = "..."  # call your LLM
        call.set_usage(input_tokens=42, output_tokens=210, finish_reason="stop")

    safe_answer = decision.guard_output_final(answer)        # Presidio + NeMo
```

That's the full wrapping. One span lands in your collector per agent
turn, tagged with everything a reviewer or auditor needs to
reconstruct the decision. Drop `guard_input` / `guard_output_final`
if you haven't wired the sidecars yet — the calls fail loud with
`GuardrailNotConfiguredError` by design, so compliance never silently
regresses.

**Prefer to see it run first?** The reference agent exercises every
SDK surface in one turn, offline, no API keys:

```bash
git clone https://github.com/singleaxis/singleaxis-fabric.git
cd singleaxis-fabric/examples/reference-agent
uv sync
uv run fabric-reference-agent --prompt "Hello"
uv run fabric-reference-agent --prompt "Hello" --low-score   # escalation path
```

Output shape: `{"response": "...", "escalated": bool, "blocked": bool,
"trace_id": "<32-hex>"}`.

## Deploy the OSS data plane

For any cluster that will take real traffic, install the umbrella
Helm chart. Regulatory profiles preset safe defaults.

```bash
git clone https://github.com/singleaxis/singleaxis-fabric.git
cd singleaxis-fabric/charts/fabric
helm dependency build

# Bare install — the OTel Collector comes up with no further flags:
helm install fabric . \
    --namespace fabric-system --create-namespace

# Dev / evaluation cluster:
helm install fabric . \
    --namespace fabric-system --create-namespace \
    --values profiles/permissive-dev.yaml

# EU AI Act high-risk posture (requires the documented Secrets,
# exporter endpoint, and release verification key):
helm upgrade --install fabric . \
    --namespace fabric-system --create-namespace \
    --values profiles/eu-ai-act-high-risk.yaml \
    --set tenant.id=TENANT_UUID \
    --set otel-collector.exporter.endpoint=https://otlp.example.com \
    --set 'update-agent.config.trustedKeys[0].publicKey=BASE64_ED25519_PUBLIC_KEY' \
    --set 'otel-collector.networkPolicy.exporterEgress.to[0].ipBlock.cidr=203.0.113.10/32' \
    --set 'otel-collector.networkPolicy.exporterEgress.ports[0].protocol=TCP' \
    --set 'otel-collector.networkPolicy.exporterEgress.ports[0].port=443'
```

**What a bare install does.** The collector installs and
starts receiving. With no `otel-collector.exporter.endpoint` set, it
writes spans to its own pod stdout (`kubectl logs`) via the OTel debug
exporter, and `NOTES.txt` prints a prominent warning saying so. Nothing is
silently dropped — but **stdout is not durable and is not an audit
trail**. Point it at a real backend before you rely on retention:

```bash
helm upgrade fabric . --reuse-values \
    --set otel-collector.exporter.endpoint=https://<your-otlp-backend>
```

Everything else — Presidio and NeMo sidecars, Langfuse, the red-team
runner, the update agent — stays **opt-in**, because each needs a
secret, a key, or an external database you have to supply. The
`langfuse` subchart in particular does **not** bundle Postgres; see
[`docs/exporting-to-your-observability-backend.md`](docs/exporting-to-your-observability-backend.md).

The example CIDR is documentation-only. Replace it with an approved backend or
egress-gateway CIDR. The high-risk profile **fails closed**. Before installing
it, provision the five operator-owned Secrets named by the profile: receiver
TLS identity, trusted client CA, Presidio tenant HMAC key, deterministic
sampler key, and outbound OTLP authorization value. The profile embeds the
redactor beside the Collector over a pod-local Unix socket; it does not depend
on the standalone Presidio Deployment. The complete prerequisite and
render-review procedure is in
[`docs/deployment.md`](docs/deployment.md).

The umbrella chart also publishes as a cosign-signed OCI artifact at
`oci://ghcr.io/singleaxis/charts/fabric` on each release. Subcharts
are bundled inside it and are not published or installable on their
own.

Chart contents, profiles, and latency posture: [`charts/fabric/README.md`](charts/fabric/README.md).
Full deployment guide including HA, DR, and upgrade posture:
[`docs/deployment.md`](docs/deployment.md).

## How it fits together

```text
      agent pod
  ┌─────────────────────────────────────────────┐
  │  your agent code                            │
  │      │                                      │
  │      ▼                                      │
  │  fabric.Decision  ──UDS──▶  Presidio sidecar│
  │      │                                      │
  │      ├──UDS─────────────▶  NeMo Guardrails  │
  │      │                                      │
  │      └─ async OTLP ─┐                       │
  └─────────────────────┼───────────────────────┘
                        ▼
                 OTel Collector  ──▶  Langfuse / Tempo / Jaeger /
                                      Honeycomb / Datadog / your sink
```

The installable OSS spine is **Connect → Observe → Relay**: SDKs, adapters or
OTLP receivers capture activity; the Collector normalizes and privacy-filters
it; the Relay role exports approved telemetry. Optional **Control** components
run beside protected model or tool calls. **Assurance** produces correlated
test and evaluation findings. **Management** configures the deployment, while
**Governance** consumes the stream to investigate, replay, and retain evidence.
Only explicitly selected controls belong in the request path.

Two-page mental model: [`docs/architecture.md`](docs/architecture.md).
Authoritative design: [`specs/002-architecture.md`](specs/002-architecture.md).

## Status

**Beta — v0.7.x.** The `specs/` directory is the design of record.
What's in this repo runs and is tested; anything marked "roadmap" is
explicitly called out. We'd rather under-document than overclaim.

Two things worth knowing before you plan around them:

- **The TypeScript SDK is not at parity with the Python SDK, and is not
  a drop-in substitute.** It implements the public emit-surface contract and
  reproduces all 39 shared conformance fixtures, but it has no framework
  adapters, guardrail or policy transports, or host-side I/O helpers. Tagged
  releases package and smoke-test its ESM, CommonJS, and type-declaration
  surfaces; feature-parity work remains tracked
  in [`docs/typescript-parity-backlog.md`](docs/typescript-parity-backlog.md).
  There is no Go SDK.
- **Latency figures in this README are design budgets, not measured
  P99s.** The benchmark suite is opt-in and is not a CI gate.

See [`CHANGELOG.md`](CHANGELOG.md) for what's in the current release.

## Quality and testing

The SDK is tested across Python 3.11–3.13 with an 85% coverage floor,
plus:

- a **schema conformance suite** with golden fixtures and a JSON Schema
  that freeze the emitted `fabric.*` / `gen_ai.*` span contract, so wire
  drift fails CI;
- a **reusable adapter-conformance kit** for verifying third-party
  guardrail / policy / transport / authorizer adapters against the
  Protocol contracts;
- an opt-in **micro-benchmark suite** and **soak harness** (informational
  and machine-dependent — no timing pass/fail threshold);
- a **live end-to-end gate** in CI: a real SDK `Decision` is exported
  over OTLP to an in-cluster collector and the `fabric.decision` span
  (plus a dynamically named GenAI child and the `fabric.tenant_id`
  attribute) is asserted to land intact.

## What this OSS distribution covers

The open-source Fabric (this repository) is the reconstruction and secure
delivery substrate for an AI agent:

- **Connect / Observe / Relay** — identity propagation, decision and
  interaction records, public activity contracts, privacy filtering,
  correlation, durable queueing, and authenticated OTLP export.
- **Optional Control building blocks** — fail-loud guardrail and PII clients,
  policy/tool-authorization events, reference sidecars, and escalation
  emission. These are selected by profile; they are not prerequisites for
  basic observation.
- **Optional Assurance interfaces** — local judge adapters, red-team runner
  packaging, and correlated finding emission. Test execution is not part of
  telemetry transport.

It is a substrate, not a certification product. **Fabric does not issue
certifications or turn a telemetry queue into an immutable audit trail.** A
customer-owned backend can consume the public contract and build its own
Decision Graph and evidence workflows. The SingleAxis Platform provides the
managed or privately deployed Management, Assurance orchestration, and
Governance implementation—including fleet configuration, investigations,
evidence, retention, and controlled replay.

Control mappings (Fabric artifact → regulatory control) are roadmap.
The structure each mapping will follow is captured in
[`specs/009-compliance-mapping.md`](specs/009-compliance-mapping.md);
nothing authoritative ships in this release. Initial targets are the
EU AI Act, NIST AI RMF, and ISO/IEC 42001; SR 11-7, HIPAA, and GDPR
follow.

## Documentation

| If you want to... | Read |
|-------------------|------|
| Install the SDK and instrument one agent turn in 5 minutes | [`docs/quickstart.md`](docs/quickstart.md) |
| Understand the 3-layer mental model and the latency principle | [`docs/architecture.md`](docs/architecture.md) |
| Deploy the Helm chart with a regulatory profile | [`docs/deployment.md`](docs/deployment.md) |
| Run a security / procurement review (the trust overview) | [`docs/enterprise-readiness.md`](docs/enterprise-readiness.md) |
| Capture every interaction an agent has (MCP, skills, sub-agents, files…) | [`docs/capturing-interactions.md`](docs/capturing-interactions.md) |
| See what an auditor will ask, mapped to what Fabric captures | [`docs/auditor-checklist.md`](docs/auditor-checklist.md) |
| See every SDK surface exercised in one runnable file | [`examples/reference-agent/`](examples/reference-agent/) |
| Read the authoritative design of record | [`specs/`](specs/) |
| Look up an SDK symbol or environment variable | [`sdk/python/README.md`](sdk/python/README.md) |
| Plan a disaster-recovery exercise | [`docs/operations/dr.md`](docs/operations/dr.md) |

## Contributing

Contributions are welcome — patches, issues, RFCs against the specs.
Read [`CONTRIBUTING.md`](CONTRIBUTING.md) first. Fabric uses the
[Developer Certificate of Origin](https://developercertificate.org/)
(DCO): every commit must be signed off with `git commit -s`. Project
decisions follow [`GOVERNANCE.md`](GOVERNANCE.md).

Participation is governed by [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md)
(Contributor Covenant 2.1).

## Security

To report a vulnerability, follow the private disclosure process in
[`SECURITY.md`](SECURITY.md). **Do not** open a public issue for
security findings. We acknowledge receipt within 3 business days and
follow a 90-day coordinated disclosure default.

## Community and support

- **Issues** — bug reports, feature requests:
  [GitHub Issues](https://github.com/singleaxis/singleaxis-fabric/issues)
- **Discussions** — questions, show-and-tell, design RFCs:
  [GitHub Discussions](https://github.com/singleaxis/singleaxis-fabric/discussions)
- **Commercial support** — for regulated deployments and managed
  operations: [singleaxis.ai](https://singleaxis.ai)

## Governance

Fabric is maintained by **AI5Labs Research OPC Private Limited**
(SingleAxis) as an open project. Maintainer appointment, release
processes, and trademark policy: [`GOVERNANCE.md`](GOVERNANCE.md).

## License

Licensed under the [Apache License, Version 2.0](LICENSE). See
[`NOTICE`](NOTICE).

SingleAxis, SASF, and the Fabric word mark are trademarks of AI5Labs
Research OPC Private Limited. The trademarks are **not** licensed under
Apache-2.0; see [`GOVERNANCE.md`](GOVERNANCE.md) for the trademark policy.
