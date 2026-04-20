---
title: Design Partner Engagement Model
status: draft
revision: 1
last_updated: 2026-04-20
owner: project-lead
---

# 013 — Design Partner Engagement Model

## Summary

SingleAxis is in Phase 1 (see [012](012-oss-commercialization-strategy.md):
zero to three paying customers). This spec describes the engagement
shape for **design partners** — the first three enterprises that commit
to deploying Fabric with SingleAxis services and agree to pattern-share
in exchange for discounted pricing and direct engineering access.

Design partnerships are the instrument that takes us from zero to three
customers. Everything else in Phase 1 — Layer 1 OSS adoption, Layer 2
internal tooling, the pitch positioning — is in service of closing
design partners.

## Goals

1. **Convert OSS adoption into first three paying engagements.** The
   SDK and reference agent generate awareness. The design partner
   program converts awareness into revenue.
2. **Extract patterns from real regulated workflows.** Design partners
   provide the production data that calibrates rubrics, refines
   playbooks, and seeds Layer 2 pattern libraries. This is the services
   flywheel.
3. **Build named customer proof.** Once three regulated enterprises are
   in production with Fabric, pitch resistance drops sharply for
   customers four through thirty.
4. **Stay honest about the exchange.** Design partners give us pattern
   knowledge; in return they get discounted pricing, direct engineering
   access, and roadmap input. No pretense otherwise.

## Non-goals

1. **Not a free consulting offer.** Design partners are paying
   customers. The discount reflects the pattern-sharing exchange and
   the higher uncertainty of early-stage engagement — not generosity.
2. **Not a free trial of Fabric OSS.** Fabric Layer 1 is free for
   everyone. The design partner program is specifically about Layer 2
   services delivery (implementation, rubric authoring, managed
   operations, evidence preparation).
3. **Not an open-ended advisory role.** Each engagement has defined
   stages and exit criteria. Nothing is "as long as you want us."
4. **Not a path to free Layer 3 product.** When Layer 3 productizes in
   Phase 3, design partners get preferential terms on migration — not
   a free license.

## Design — target partner profile

We want partners with these characteristics:

| Dimension | Criterion |
|---|---|
| Industry | Regulated: banking, insurance, healthcare, pharma, legal, regulated public sector |
| Agent use case | Concrete and scoped, with production intent — not pure research |
| Horizon | 6–12 months to production, not 24+ |
| Internal sponsor | Engineering lead + Compliance/GRC lead both bought in |
| Headcount committed | 1–3 engineers + 1 compliance contact |
| Regulatory driver | Upcoming audit, new jurisdiction, board ask, or enforcement date (e.g., EU AI Act Aug 2026) |
| Data posture | Willing to run Fabric in their VPC; raw content never egresses |

We specifically do **not** want:

- Research labs without production intent
- Companies wanting Fabric OSS "for free" with implicit services
  expectation
- Pure advisory engagements ("review our architecture")
- Unregulated startups wanting agents — the wedge is regulatory, not
  generic

## Design — what the partner uses

A design partner deploys and operates three layers of surface.

### Layer 1 (Fabric OSS, Apache-2.0)

- Fabric SDK in their agent codebase (wraps agent calls in `Decision`,
  records guardrails + retrieval + escalation)
- Framework adapter for their orchestration choice (LangGraph, Agent
  Framework, or CrewAI)
- Presidio + NeMo Guardrails sidecars as UDS services alongside their
  agent
- OTel Collector distribution with the Fabric-standard processor chain
- Reference agent as a starting architecture

They *could* do all of this without us. The OSS is free, documented,
and deployable. That's the "genuinely useful standalone" test from
spec 012.

### Layer 2 (SingleAxis services, not distributed)

- Regulatory mapping from their specific use case to concrete controls
- Rubric authoring for their domain (factuality, PII, safety,
  vertical-specific)
- Implementation engineering — wiring Fabric into their existing
  observability, IAM, secrets, and model endpoint
- Operator training for their SRE team
- Managed rubric updates as regulations evolve
- Pattern detection across their own trace corpus
- Incident response when guardrails or judges flag a decision
- Evidence preparation when their first audit lands

This is what they pay for. Layer 1 gets them to "an agent that runs
through Fabric." Layer 2 gets them to "an agent that passes audit."

### Layer 3 (future product, Phase 3)

Evidence bundles, signed attestation, hosted archive — these arrive in
Phase 3. Design partners get the manual equivalent from SingleAxis
services in Phase 1–2, which naturally productizes into Layer 3 as
patterns repeat across partners.

## Design — value to the partner

What they get in return for being design partner #1, #2, #3:

1. **Faster time to audit-ready production.** Weeks-to-months
   compression vs. building alone.
2. **Pre-mapped regulatory posture.** Our vertical playbook covers
   their specific regulatory surface; they don't start from zero.
3. **Direct engineering access.** Their engineering team has a line to
   our team, not a ticketing system.
4. **Roadmap input.** What they need shapes what we prioritize,
   within scope.
5. **Discounted Phase 1 pricing.** Early-customer rate. Structure:
   engagement fee + monthly managed-service retainer, discounted from
   the rate later customers will pay.
6. **Named early-customer status.** Optional case study or reference
   call at their discretion. No logos without consent.

## Design — what SingleAxis asks in return

Design partners exchange pattern knowledge for preferential terms.
This is explicit in the engagement contract:

1. **Anonymized pattern sharing.** SingleAxis may observe (via the
   Telemetry Bridge, in the tenant's VPC, on redacted summaries only)
   failure modes, rubric effectiveness, and guardrail hit patterns.
   These observations improve our rubrics and playbooks for all
   future customers. Raw content never leaves their VPC.
2. **Access to compliance-team interpretations.** When their GRC or
   legal counsel interprets a regulation for their use case, we learn
   from that interpretation (abstracted, not attributable).
3. **Rubric co-design participation.** For their vertical, they help
   author the first version of the domain-specific rubric. We retain
   the rubric for use with future customers in the same vertical.
4. **Case study permission (optional).** Anonymized patterns are used
   regardless; named case studies are opt-in.

None of this is hidden. The engagement contract names the exchange
explicitly.

## Design — engagement stages

Each design partner engagement runs through four stages. Durations are
illustrative.

### Stage A — Discovery (2–3 weeks)

- Map their agent use case against applicable regulations
- Identify the audit surface (who will ask, what will they ask, when)
- Confirm technical fit (model endpoint, orchestration, infra posture)
- Scope the engagement — what's in, what's out
- **Exit:** signed engagement scope + approved architecture

### Stage B — Implementation (6–12 weeks)

- Deploy Fabric OSS (Layer 1) in their environment
- Configure for their specific workflow — guardrail policies, OTel
  routing, orchestration adapter
- Stand up internal SingleAxis tooling (Layer 2) for rubric authoring
  and pattern observation
- Wire into their existing observability stack
- Run the first end-to-end agent call through Fabric
- **Exit:** reference agent passes end-to-end; one production-like
  workflow runs through Fabric with guardrails active

### Stage C — Hardening (4–8 weeks)

- Author vertical-specific rubrics
- Run the first judge-scoring passes on real traces
- Handle first guardrail / judge / escalation events in production
- Calibrate pattern detection
- Train their SRE team on operating Fabric
- **Exit:** their agent is live with Fabric controls active, their
  team can operate it, first evidence artifacts exist

### Stage D — Managed operation (ongoing, minimum 12 months)

- Monthly rubric updates (regulatory drift, pattern library growth)
- Incident response for flagged decisions
- Pattern observation for their trace corpus
- Evidence preparation when an audit arrives
- Quarterly business review on coverage and new use cases
- **Exit:** mutual termination clause; minimum 12 months, then
  month-to-month

## Design — commercial structure shape

This spec does not name dollar amounts (negotiated per partner). The
structure is:

1. **Stage A onboarding fee** — one-time, covers discovery scope.
   Credited against Stage B if the partner proceeds.
2. **Stage B/C engagement fee** — fixed-price for implementation and
   hardening. Milestone-based billing.
3. **Stage D managed-service retainer** — monthly. Covers rubric
   updates, pattern observation, incident response, and an operator
   hours bucket.
4. **Per-agent scaling** — as they deploy more agents under Fabric,
   pricing scales per agent (not per request).
5. **Evidence preparation** — included in retainer for the first
   audit; per-audit pricing thereafter.

Design partners 1–3 receive preferential multipliers across all of the
above. Specific discounts are documented in each engagement contract,
not in this spec.

## Design — closing a design partner

The close motion at a high level:

1. **Awareness** — prospect finds Fabric via OSS adoption, conference
   talk, or direct outreach.
2. **Qualification call** — confirm industry, use case, horizon,
   sponsor strength. Disqualify fast if criteria are not met.
3. **Technical deep-dive** — our engineering + their engineering spend
   2–4 hours on architecture fit. Both sides know within that session
   whether this works.
4. **Compliance deep-dive** — our playbook lead + their GRC/compliance
   counterpart walk through regulatory mapping for their use case.
5. **Engagement proposal** — Stage A scope + full-engagement shape +
   commercial terms in one document.
6. **Design partner contract** — includes pattern-sharing clause,
   discount structure, exit terms.
7. **Stage A kickoff.**

Target cycle from first conversation to Stage A kickoff: 4–8 weeks.

## Security considerations

The design partner relationship introduces one new data-path:
SingleAxis, via the Telemetry Bridge, may receive sanitized summaries
from the partner's environment. This is the same Bridge described in
spec [004](004-telemetry-bridge.md); the design partner relationship
does not create any new mechanism.

- **Raw content never egresses.** Same as any Fabric deployment.
- **Redaction is audit-verifiable.** The partner's security team can
  verify the Bridge's redaction policy before activating egress.
- **Dry-run mode available.** Partners may operate in dry-run (no
  egress) while trust is established, then switch to active mode.
- **Pattern observations are anonymized.** Nothing attributable to the
  partner is used for another customer's rubric without explicit
  consent.

## Operational considerations

- **Capacity cap:** SingleAxis can run at most 3 design partner
  engagements concurrently while staying above quality bar. Do not
  oversell Phase 1.
- **Reference calls:** each design partner is asked at month 6 and
  month 12 whether they are willing to take reference calls. Not
  required; tracked in partner file.
- **Phase 1 exit criteria:** three design partners in Stage D
  (managed operation), each with at least one live agent, each having
  gone through at least one internal compliance review.
- **Transition to Phase 2:** begins when design partner #3 reaches
  Stage D. Phase 2 relaxes the pattern-sharing exchange (more
  transactional pricing, no discount) and broadens target profile.

## Open questions

- **Q1.** Do we narrow to one target industry for Phase 1 (e.g.,
  healthcare AI), or stay cross-industry? *Resolver: project lead +
  commercial advisor. Deadline: before first partner outreach.*
- **Q2.** Is the SingleAxis "playbook lead" role billable time or
  fixed-cost? *Resolver: project lead + commercial advisor. Deadline:
  before Stage A scoping.*
- **Q3.** Do we offer a "design partner pass" — discounted future
  engagement fee for OSS Layer 1 adopters on the path to services
  conversion? *Resolver: project lead. Deadline: before first
  outreach.*
- **Q4.** What's the SLA shape for Stage D (response time on flagged
  decisions, business hours vs. 24/7)? *Resolver: project lead +
  security lead. Deadline: before first partner contract.*

## References

- [012 — OSS Distribution & Commercialization Strategy](012-oss-commercialization-strategy.md)
- [001 — Product Vision & Positioning](001-product-vision.md)
- [004 — Telemetry Bridge](004-telemetry-bridge.md) — data path for
  pattern observation
- [007 — Escalation Workflow](007-escalation-workflow.md) — HITL
  mechanism used in Stage D
