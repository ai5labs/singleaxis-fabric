---
title: Phased Execution Roadmap
status: draft
revision: 4
last_updated: 2026-07-25
owner: project-lead
---

# 011 — Roadmap

## Summary

Fabric is built incrementally, by capability tier. Each phase is
gated on **technical and ecosystem milestones**, not calendar time
or feature completeness against any specific competitor. Where a
phase exit depends on real-world adoption ("conformance tests
exercised against N independent installations"), that signal is
named explicitly so the criterion is testable rather than
aspirational.

Status is asserted against the code, not against intent. Where a
phase deliverable did not land as written, it is named **partial**
or **not started** rather than quietly reworded, and where an exit
criterion closed unproven it is carried forward by name.
`CHANGELOG.md` is the authoritative per-release record; this spec
sequences, it does not enumerate. The current release line is
v0.7.x.

This spec stays `status: draft` permanently. The vocabulary in spec
000 defines `accepted` as "implementation may begin" and
`implemented` as "the behaviour in the code matches the spec" —
neither is meaningful for a document whose subject is work not yet
done. A roadmap is never finished, so it is never promoted.

This spec covers the **public roadmap only**. Components and
services maintained internally by SingleAxis are referenced for
context but their detailed plans are not part of this repository.

## Non-goals

- Timeline commitments with specific dates. Sequencing matters;
  calendar time depends on headcount, scope decisions, and ecosystem
  signals not yet observed.
- Feature parity with any specific competitor. Fabric is opinionated;
  we ship what fits the architecture.
- Publishing the roadmap for components not in this repository.
- Duplicating `CHANGELOG.md`. A phase says what class of work is in
  flight; the changelog says what shipped in which release.

## Phase 0 — Scaffolding & specs (complete)

**Goal:** publish the design of record and the repo structure.

**Status:** complete. The specs directory is at revision 1+ and the
root governance files (`LICENSE`, `CONTRIBUTING.md`, `SECURITY.md`,
`GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, `MAINTAINERS.md`) are in
place.

## Phase 1 — Foundation (v0.1.x — complete)

**Goal:** ship the L2 (tracing) + L4 (red-team) + L5 (guardrails)
layers of the agent stack as a coherent OSS substrate that an
enterprise platform team can install and operate without
hand-holding.

**Status:** complete. Every public deliverable below shipped in the
v0.1.x line. One exit criterion closed unproven and is assessed
honestly at the end of this phase.

### Scope (the L1 OSS product)

Fabric L1 OSS = **L2 Agent Tracing + L4 Red-Teaming runner + L5
Inline Guardrails + L1 Orchestration adapters** of the 8-layer
agent stack defined in spec 002. Layers L3 (observability backend),
L6 (LLM-as-Judge), L7 (Security/IAM), and L8 (Context Sources) are
either tenant-owned (L3, L7, L8) or part of the SingleAxis
commercial control plane (L6).

### Public deliverables (Apache-2.0)

- **Fabric SDK (Python):**
  - `Fabric` client, `Decision` context manager
  - Inline guardrail chain (Presidio + NeMo rails)
  - Retrieval recording (records local hashes; the L2 commercial
    Decision Graph materializes the provenance — spec 003)
  - Escalation pause primitive (the SDK exception + adapters; SASF
    review service is L2 — spec 007)
  - Adapters: LangGraph, Microsoft Agent Framework, CrewAI
- **Guardrail sidecars:** Presidio (UDS), NeMo Guardrails (UDS)
- **OTel Collector distribution** with Fabric processors
- **Red-team runner** (L4): Garak/PyRIT/Promptfoo CronJob
- **Reference agent** (end-to-end SDK exerciser)
- **Helm chart** with profiles: `permissive-dev` and
  `eu-ai-act-high-risk`

### NOT in this repo (referenced for clarity)

The following are described in specs 003/004/006/007/009 as a public
**design of record**, but their managed implementations are not part of this
OSS distribution:

- **L6 LLM-as-Judge:** judge worker pool, signed rubric library
- **Telemetry Bridge:** sanitized egress to SingleAxis SaaS
- **Decision Graph:** materialized provenance store (Postgres+pgvector
  / Neo4j)
- **Escalation service:** SASF reviewer dashboard, signed verdict
  webhook, durable checkpoint resume
- **Evidence Bundle exporter:** signed compliance bundles per spec
  009

### Exit criteria (Phase 1) — assessed

- **Public OSS installable without hand-holding — met.** The kind
  quickstart runs end-to-end in CI (`.github/workflows/e2e.yml`, job
  "kind cluster install + smoke"), and that job asserts a live SDK
  span reaches the in-cluster collector over OTLP —
  `fabric.decision`, `fabric.tenant_id` and a `fabric.llm_call`
  child span must all appear in the collector's own stdout — rather
  than exercising only an in-memory exporter.
- **Decision-span contract enforced — met, with one gap named.**
  The contract is frozen as a JSON Schema plus 39 golden fixtures
  (`sdk/python/tests/conformance/`) and enforced on every Python PR.
  The criterion as originally written named the *reference agent*
  specifically; `examples/reference-agent/` carries its own test
  suite but is not invoked by any workflow. What CI actually proves
  is the SDK contract plus the `examples/e2e-smoke/` flow. That is
  the stronger guarantee, but it is not the literal criterion, so
  the difference is recorded rather than glossed.
- **Released artifacts signed with SBOMs — met.**
  `.github/workflows/release.yml` generates SPDX and CycloneDX
  SBOMs via `anchore/sbom-action`, cosign-signs the source archive
  and both SBOMs keyless (Fulcio, `id-token: write`), cosign-signs
  every published image, and attaches build-provenance
  attestations.
- **Inline guardrail latencies meet the published spec 005 P99
  budgets under representative load — not verified.** The benchmark
  suite exists but is opt-in and informational
  (`python -m benchmarks.run`); it lives outside `tests/`, has no
  pass/fail threshold, and appears in no workflow. Spec 005
  §Enforcement was corrected in v0.5.0 to say exactly that: the
  budget is a design target, not a CI-enforced SLO. We do not claim
  a measurement we do not take.

Phase 1 is closed on the first three criteria. The fourth carries
forward as a named Phase 3 item rather than being quietly dropped.

## Phase 2 — Capture-everything SDK + observable-by-default chart (v0.2.x–v0.6.x)

**Goal:** earn the "open-source observability and control plane for
AI agents" framing by emitting traces that observability backends
actually render natively, and by ensuring the chart's default
deployment puts spans into a real backend.

**Status:** substantially delivered, unevenly. The SDK half overran
its brief — v0.4.0 through v0.6.0 added primitives that were never
in this phase's list (policy evaluation, judge queueing, memory
lineage and erasure, the execution/step taxonomy, replay metadata,
surface and interaction capture). The chart half moved late and
only partway: the collector now installs by default rather than
rendering nothing, but its default destination is the collector
pod's own stdout, which is visible, not durable, and not a
backend. The second-language half did not keep pace. Each
deliverable below carries its actual state.

### Public additions

- **`Decision.llm_call` + `Decision.tool_call` — shipped (v0.2.0).**
  Child-span context managers writing standard `gen_ai.*` semantic
  conventions alongside `fabric.*` extensions
  (`sdk/python/src/fabric/_calls.py`). Extended in v0.6.0 with
  cache, streaming, retry and idempotency setters and a canonical
  `ToolErrorCategory` enum. Backends keyed on the `gen_ai.*`
  conventions have the attributes they need; whether a given
  backend's UI renders them is a separate claim, assessed under
  Exit below.
- **Auto-instrument extras — shipped (v0.2.0).** Five extras wrap
  the upstream `opentelemetry-instrumentation-*` packages with
  Fabric's content-redaction guard, defaulting to no raw prompt or
  completion on the span: `[openai]`, `[anthropic]`, `[bedrock]`,
  `[otel-langchain]`, `[cohere]`. Earlier revisions of this spec
  listed `[langchain]`, `[langgraph]` and `[crewai]` here. The
  first is named `[otel-langchain]`; the other two are
  orchestration **adapter** extras, not auto-instrumentation — a
  distinction this spec previously blurred.
- **Collector trace processors — shipped in the current line.**
  `fabricguard` registered a traces variant alongside logs from the
  start; the current unreleased work adds traces variants to
  `fabricsampler`, `fabricredact` and `fabricpolicy`
  (`*/factory.go` now register both), and the chart chains all four
  into the `traces:` pipeline under the same toggles as logs. This
  closes the gap this section previously retracted: field
  allowlisting, HMAC sampling, Presidio redaction and policy
  enforcement now apply to spans, not just log records. The Phase 3
  carry-forward item is done; what remains open here is only the
  executed verification (see Phase 3 "backend render verification").
- **Bundled-Langfuse default exporter — not shipped as described;
  a narrower version of the goal landed instead.**
  `charts/fabric/charts/langfuse/` ships a minimal wrapper, but
  `langfuse.enabled` defaults to `false` (it needs an external
  Postgres DSN and fail-closes at render without one) and no
  template wires the collector to it. This spec previously stated
  `langfuse.enabled: true (chart default)`, which
  `charts/fabric/values.yaml` contradicts; that claim is
  retracted. What did land, in the current unreleased line, is the
  half of the goal that does not depend on Langfuse:
  `otelCollector.enabled` now defaults to `true`, so a bare
  `helm install` no longer renders zero resources, and with no
  `exporter.endpoint` set the pipeline falls back to the debug
  exporter and announces it. That makes the default install
  *observable* — spans are readable via `kubectl logs` — but not
  *exported*. Stdout is not a backend. Automatic wiring to a real
  default sink moves to Phase 3.
- **Additional SDK languages — one partial, one not started.**
  - *TypeScript — partial, and not yet released.* `sdk/typescript/`
    builds `@singleaxis/fabric` against the same wire contract and
    is validated against the same shared goldens. It is a capture
    library by design — seven modules against the Python SDK's
    fifty-eight — and reproduces 19 of the 39 shared goldens.
    Outstanding work is tracked in
    `docs/typescript-parity-backlog.md` (four open items: step
    taxonomy, ReplayMetadata, expanded conformance coverage, LLM
    and tool-call telemetry). Its CI job is advisory and
    path-filtered to `sdk/typescript/**`, so a Python-only PR that
    adds a golden does not fail it. The npm publish workflow fires
    only on a `ts-v*` tag and no such tag exists, so the package is
    in-tree but unpublished. A TypeScript SDK exists; TypeScript
    parity does not, and this spec should not have implied
    otherwise.
  - *Go — not started.* There is no `sdk/go`.
- **Rails library — not started.** `components/nemo-sidecar/rails/`
  holds a single `starter` bundle (three files). There is no
  per-regulatory-profile Colang catalog.
- **Conformance tests — shipped for Python.** 39 frozen goldens
  plus a formal JSON Schema at `SCHEMA_VERSION` 1.0, enforced on
  every Python PR, with an adapter-conformance kit alongside. One
  caveat against the criterion as written: `tests/` ships in the
  sdist and the repo but not the wheel, so this is a wire-contract
  suite for contributors and SDK authors, not yet the packaged
  "verify my installation" suite a tenant runs against their own
  deployment. That packaging is a Phase 3 item.

### Entry / Exit

- **Entry: met**, with the guardrail-latency caveat recorded in
  Phase 1.
- **Exit: not met**, on both criteria.
  - *Fabric traces render natively in Phoenix + Langfuse +
    Honeycomb + Datadog (verified by smoke renders).* We document
    the Helm wire-up for each backend
    (`docs/exporting-to-your-observability-backend.md`) and the
    spans carry the `gen_ai.*` conventions those backends key off,
    but no smoke render is executed or recorded in any workflow.
    Documented is not verified.
  - *At least three independent organizations have published
    Fabric-instrumented agents.* Not observed. This is an adoption
    signal, not an engineering task; it is reported when it
    happens and not before.

## Phase 3 — Parity, profile breadth & operability

**Goal:** close the gaps Phase 2 left open, so that what the chart
and the docs promise is what an operator actually gets — in more
than one language and more than one regulatory posture.

This phase contains almost no new product surface. It is the cost
of claims already made.

### Public additions

- **TypeScript parity.** Reproduce the remaining shared goldens (20
  of 39 currently unreproduced) per
  `docs/typescript-parity-backlog.md`, and promote the
  `typescript-sdk` CI job from advisory and path-filtered to a
  required check, so a new Python golden cannot land without
  matching TypeScript coverage. Parity here means the emitted wire
  contract, not the host-side integration surface, which stays
  Python-only per `sdk/python/SCOPE.md`.
- **Go SDK.** A capture-core module on the same terms TypeScript was
  accepted on: same wire contract, validated against the same
  shared goldens, versioned independently, host-side I/O out of
  scope.
- **Remaining collector processors on traces — done** in the current
  line (see Phase 2, "Collector trace processors"). Exit criterion
  "all four collector processors handle traces" is met; verification
  of the end-to-end render is tracked under "backend render
  verification" below.
- **A real default sink.** When `langfuse.enabled` is true, default
  the collector's `exporter.endpoint` to the bundled service rather
  than requiring the operator to name it — which first requires
  settling whether the bundled Langfuse version accepts OTLP at
  all, and what it needs for storage. Until then the debug-exporter
  fallback is the honest default, and the loud post-install notice
  that spans are going nowhere durable stays.
- **Regulatory profile breadth.** Spec 009 maps six regulations;
  two profiles ship (`permissive-dev`, `eu-ai-act-high-risk`).
  NIST AI RMF, ISO 42001, SR 11-7 and HIPAA have none, and GDPR is
  treated in spec 009 as a layer over any profile rather than a
  standalone posture.
- **Rails catalog by profile.** Grow `components/nemo-sidecar/rails/`
  past `starter`, organized to match the profiles.
- **Compliance-mapping posture.** `docs/compliance/mappings/` ships
  empty by design — spec 009 places the per-regulation control
  mapping documents in the commercial control plane, because they
  reference evidence the OSS distribution does not produce. What
  plausibly belongs in OSS is the other half: which `fabric.*`
  events and which chart controls each profile locks, and what an
  auditor can and cannot conclude from those alone. Settling that
  boundary against spec 009 is a precondition for this item, not
  part of it.
- **SRE runbooks.** `docs/operations/` holds a DR runbook and
  nothing else. Upgrade, rollback, key rotation, sidecar failure,
  collector backpressure and cardinality incidents each need one.
- **Guardrail latency verification.** Carried forward from Phase 1:
  an executed, recorded measurement against the spec 005 budgets,
  published alongside that spec.
- **Backend render verification.** Carried forward from Phase 2: a
  recorded smoke render per named backend, or an honest narrowing
  of the claim to those actually checked.
- **Tenant-runnable conformance suite.** Package the conformance
  runner so an operator can point it at their own installation,
  rather than it being reachable only from a repo checkout.

### Entry / Exit

- **Entry:** Phase 2's additions are either shipped or explicitly
  reclassified, and its unmet exit criteria are carried forward
  here by name. Done as of this revision.
- **Exit:** the TypeScript SDK reproduces every shared golden under
  a required CI check; a Go SDK does the same; all four collector
  processors handle traces; every profile named in spec 009 either
  ships or is explicitly declined with a stated reason;
  `docs/operations/` covers the incident classes above; the
  guardrail-latency and backend-render measurements carried from
  Phases 1 and 2 are executed and published.

## Phase 4 — Stability & general availability

**Goal:** a stable, widely-deployed substrate with API commitments
that production users can pin against.

### Public additions

- **API stability commitments** (SDK, OTel attribute wire schema).
  The policy document shipped in v0.5.0
  (`docs/api-stability.md`); what remains is the 1.0 commitment
  itself.
- **Long-term support branches** for minor releases
- **Expanded adapter surface** as new orchestration frameworks
  emerge
- **First-class OpenShift, GKE, EKS recipes**

### Exit criteria

SRE runbooks moved to Phase 3 — they are owed against the current
release, not against 1.0.

- Used in production by at least five tenants in regulated sectors
  running the OSS standalone
- A recognised regulator or auditor cites Fabric by name in
  published guidance
- Documented upgrade paths from earlier versions
- Certified partnerships with upstream components (Presidio, NeMo,
  OpenTelemetry)
- Project governance moves toward (optional) foundation neutrality
  if warranted

## Risk register

| Risk | Mitigation |
|------|------------|
| Regulation changes faster than we can ship | Signed rubric channel (operated as a service) lets policy updates flow to operators without a full chart release. |
| A foundational dependency (Presidio, NeMo, LangGraph) goes in a hostile direction | Adapter layer in the SDK isolates upstream changes; components are swappable without breaking Fabric wire contracts. |
| Open-source contribution flow fails to materialise | Deep integration partnerships (OpenTelemetry, Presidio, NeMo) substitute for community-maintainer model. |
| Competitive closed platforms commoditise "compliance" framing | Fabric's wedge is the open substrate combined with the attestation network. A closed platform cannot credibly offer the OSS; a non-attested OSS cannot offer the verdict. |
| Second-language SDKs drift behind the Python reference | The shared golden fixtures are the single contract, not prose. Phase 3 promotes the TypeScript conformance job to a required check so a new Python golden cannot merge without matching coverage. |
| This roadmap drifts from the code again | Each revision asserts phase status against cited code paths and CI workflows rather than intent. `CHANGELOG.md` remains the authoritative per-release record. |
| Maintainer bench stays thin | `MAINTAINERS.md` records a single maintainer across every component. Scope discipline — the L1/L2 boundary in spec 012 — is the primary mitigation; growing the bench is a standing priority. |

## References

- [001 — Product Vision & Positioning](001-product-vision.md)
- [000 — Overview & Conventions](000-overview.md)
- [008 — Deployment Model](008-deployment-model.md)
- [009 — Compliance Mapping](009-compliance-mapping.md)
- [012 — OSS Commercialization Strategy](012-oss-commercialization-strategy.md)
- [CHANGELOG.md](../CHANGELOG.md)
- [TypeScript parity backlog](../docs/typescript-parity-backlog.md)
- [API stability policy](../docs/api-stability.md)
- [Regulatory profiles](../docs/regulatory-profiles.md)
