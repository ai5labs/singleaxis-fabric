---
title: Product Vision & Positioning
status: draft
revision: 2
last_updated: 2026-04-20
owner: project-lead
---

# 001 — Product Vision & Positioning

## Summary

**Fabric is the open-source substrate for audit-ready AI agents.** The
Apache-2.0 SDK, adapters, and guardrail sidecars in this repo — "Layer
1" in the commercialization model
([spec 012](012-oss-commercialization-strategy.md)) — instrument
enterprise agents with standardized decision tracing, inline PII
redaction, Colang policy rails, and a structured escalation primitive.

SingleAxis, the commercial entity behind Fabric, provides the services
and internal tooling that turn instrumented agents into audit-ready
ones: implementation, rubric authoring, managed operations, and (in
time) evidence generation and attestation. The SDK is free; the
expertise and the signature are not.

## Goals

1. **Make the right thing easy.** A mid-size enterprise platform team
   should be able to stand up a compliance-ready agent environment in
   days, not quarters.
2. **Be the default substrate.** When engineers and CISOs ask "how do
   we do this properly?" for regulated agent deployment, Fabric should
   be the expected answer.
3. **Earn auditor trust.** The artifacts Fabric produces — the Context
   Graph, evidence bundles, SASF attestations — should be what auditors
   accept as evidence for EU AI Act, NIST AI RMF, ISO 42001, SR 11-7,
   HIPAA, GDPR, and future regimes.
4. **Keep tenants sovereign.** Raw traces, content, and user data
   never egress the tenant VPC by default. Ever.
5. **Stay honest about the layer boundary.** No phone-home in the OSS.
   No retroactive relicensing of anything that ships Apache-2.0. No
   bait-and-switch on the Layer 1 surface. Layer 2 and Layer 3
   capabilities are commercial by design and never pretended to be
   free — see [spec 012](012-oss-commercialization-strategy.md).

## Non-goals

1. **Not an agent framework.** Fabric does not prescribe LangGraph over
   CrewAI over Agent Framework. Orchestration choice belongs to the
   tenant.
2. **Not an LLM.** Fabric does not ship a model. Tenants bring their own
   model endpoint (Bedrock, Azure OpenAI, vLLM, self-hosted).
3. **Not a dashboard SaaS.** Langfuse is the local observability UI;
   SingleAxis's SaaS is narrow in scope (SASF review, evidence export,
   signed updates).
4. **Not a GRC platform.** Tools like Credo AI, Holistic, and Fairly
   occupy the GRC dashboard space. Fabric produces the evidence those
   platforms can consume.
5. **Not a replacement for a security team.** Fabric gives a security
   team the controls and artifacts they need. It does not absolve them
   of ownership.

## The problem

Enterprises that want to deploy LLM agents in regulated contexts
(finance, healthcare, government, regulated-industry customer support,
legal, pharma) face three simultaneous problems:

1. **Assembly cost.** A credible stack requires at least 8 categories
   of tooling — orchestration, tracing, observability, red-teaming,
   guardrails, judges, security, context/memory. Each has 3+ viable
   open-source options. Selecting, integrating, upgrading, and
   hardening them is a multi-quarter effort that is duplicated at
   every company.
2. **Regulatory translation.** Regulations (EU AI Act Articles 9–15,
   NIST AI RMF, ISO 42001, SR 11-7) describe outcomes, not controls.
   Teams spend months mapping abstract requirements to concrete
   engineering artifacts. The mapping is non-obvious and
   non-stationary — regulations change faster than internal teams can
   adapt.
3. **Attestation credibility.** When an auditor asks "who verified
   this?", the answer "our internal team" is weaker than "a named
   third party with a published methodology." Yet no such third
   parties exist at scale for agent evaluation today.

## The solution

Fabric addresses these as one package:

1. **Assembly** — One Helm chart deploys a hardened, opinionated
   integration of the standards. Tenants replace individual components
   if they need to; the defaults are chosen to satisfy regulatory
   posture without further configuration.
2. **Translation** — Fabric ships **Regulatory Profiles** (named
   `values.yaml` presets) that map regulations to specific controls
   already implemented in the stack. A tenant selects their profile
   at install time and receives a configuration that satisfies the
   control set without bespoke integration work.
3. **Attestation** — Tenants working with SingleAxis can opt into the
   Audit Bridge: sanitized summaries stream to SingleAxis, where
   SASF human reviewers evaluate against a signed rubric library and
   issue attestation. Content stays local; verdicts are portable.

## Positioning — "Two doors"

Fabric is presented to the market as two complementary offerings, not
as two tiers of the same product:

| Door | For whom | What it contains | Economic model |
|------|----------|------------------|----------------|
| **OSS (Layer 1)** | Engineering teams instrumenting an agent for traceability and inline guardrails; platform-capable organizations | Fabric SDK + adapters + guardrail sidecars + OTel distribution + reference agent | Free (Apache-2.0). No support, no SLA, no attestation |
| **Services** (SingleAxis) | Regulated enterprises who want the outcome (audit-ready, evidenced) not the operational burden | Layer 1 + SingleAxis's internal tooling + implementation + rubric authoring + managed operations + evidence preparation | Engagement fee + monthly retainer — see [spec 013](013-design-partner-model.md) |

The OSS door is the top-of-funnel. The services door is the revenue.
The OSS door exists because:

- Engineers and CISOs trust what they can audit.
- "Used in production by X companies" on a GitHub repo beats any
  sales deck.
- Tenants who install Layer 1 themselves become services leads when
  they hit the complexity wall — vertical rubrics, regulatory mapping,
  first audit.
- Paid conversion is triggered by regulatory events (upcoming audit,
  new jurisdiction, board ask), not by software scarcity.

Layer 2 (internal tooling) and Layer 3 (future product) are not
public; see [spec 012](012-oss-commercialization-strategy.md) for the
layer boundary and rationale.

## Differentiation

| Alternative | What they do | Why Fabric wins |
|-------------|--------------|-----------------|
| Raw OSS (Langfuse + Arize + Garak + NeMo + ...) | Same components, unintegrated | Fabric is the coherent, maintained integration |
| Proprietary platforms (agent-in-a-box SaaS) | Closed stack, closed model, closed data path | Fabric keeps data in-VPC; no vendor lock on framework or model |
| GRC platforms (Credo, Holistic, Fairly) | Compliance dashboards | Fabric produces the evidence those dashboards visualize |
| Hyperscaler agent services (Bedrock Agents, Azure AI Agents) | Orchestration tied to a cloud | Fabric is cloud-agnostic; tenant can run any framework against any model |
| Internal build | Bespoke, slow, duplicated everywhere | Fabric removes the duplication; tenant focuses on their use case |

The defensible wedge is the combination of **open substrate + human
attestation network (SASF)**. The substrate earns trust; the attestation
earns revenue. Neither alone is the product.

## Target users

Primary:

- **Platform engineering leads** at regulated enterprises deploying
  agents for internal tooling or external customer products.
- **AI/ML platform teams** standing up their first production agent.
- **CISOs and VP Risk** needing defensible deployment posture.
- **Compliance and regulatory affairs leads** needing evidence bundles.

Secondary:

- **MLOps and observability engineers** adopting the stack for
  non-regulated workloads because the integration is simply good.
- **Consultancies and integrators** using Fabric as a delivery vehicle
  for their engagements (encouraged — see Governance).

## Economic shape for SingleAxis

Fabric changes the economic shape of SingleAxis's existing consulting,
on a phased schedule tied to customer count
([spec 012](012-oss-commercialization-strategy.md)):

- **Phase 1 (0–3 customers, now):** revenue is implementation services.
  Fabric Layer 1 is the lead funnel; Layer 2 internal tooling makes
  each engagement efficient. Services margins — same business, better
  delivery instrument.
- **Phase 2 (3–10 customers):** patterns repeat across customers. Some
  Layer 2 tooling is billable as managed-service retainer. Revenue
  mix shifts toward recurring.
- **Phase 3 (10+ customers):** Layer 3 productizes. Evidence bundles,
  compliance dashboards, and attestation ship as a named commercial
  product at product margins.

Fabric is not a new revenue line so much as a better shape for the
existing one — with product-tier economics on the horizon as the
services business accumulates reusable learning.

## The honest one-liner

> *"Fabric is the open-source substrate for audit-ready AI agents.
> SingleAxis is the team that installs it, tunes it for the regulation
> that matters to you, and (in time) signs off on what it produces."*

## Open questions

- **Q1.** Does Fabric commit to support for ≥2 orchestration frameworks
  from day one, or launch with a single best-in-class default?
  *Resolver: project lead. Deadline: before 0.1.0.*
- **Q2.** Is the Audit Bridge a separable OSS component (so tenants
  can verify it independently) or bundled into the core chart?
  *Resolver: project lead + security maintainer. Deadline: before
  0.1.0.*
- **Q3.** What is the first Regulatory Profile shipped? (Recommended:
  EU AI Act high-risk — broadest international attention.)
  *Resolver: project lead + compliance advisor. Deadline: before
  0.2.0.*

## References

- [EU AI Act, Regulation (EU) 2024/1689](https://eur-lex.europa.eu/eli/reg/2024/1689/oj)
- [NIST AI Risk Management Framework 1.0](https://www.nist.gov/itl/ai-risk-management-framework)
- [ISO/IEC 42001:2023 AI Management System](https://www.iso.org/standard/42001)
- [SR 11-7 — Guidance on Model Risk Management](https://www.federalreserve.gov/supervisionreg/srletters/sr1107.htm)
