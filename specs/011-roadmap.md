---
title: Phased Execution Roadmap
status: draft
revision: 2
last_updated: 2026-04-20
owner: project-lead
---

# 011 — Roadmap

## Summary

Fabric is built incrementally in phases. Each phase is gated on
**customer signal** (SingleAxis's own ***REDACTED***), not
calendar time or feature completeness. This reflects the
commercialization strategy in
[spec 012](012-oss-commercialization-strategy.md): OSS adoption is
pull, not push; internal tooling matures against real workflows;
product tier ships when market demand is real.

This spec covers the **public Layer 1 roadmap only**. Layer 2
(SingleAxis internal tooling) and Layer 3 (future commercial product)
are referenced for context but their detailed plans live in
SingleAxis-internal documentation, not in this repo.

## Non-goals

- Timeline commitments with specific dates. Sequencing matters;
  calendar time depends on headcount, customer pipeline, and scope
  decisions not yet made.
- Feature parity with any specific competitor. Fabric Layer 1 is
  opinionated; we ship what fits the layer model.
- Publishing the Layer 2 or Layer 3 roadmap. Those plans are
  commercial artifacts, not commitments to the open-source community.

## Phase 0 — Scaffolding & specs (complete)

**Goal:** publish the design of record and the repo structure.

**Status:** complete. The specs directory is at revision 1+ and the
root governance files (`LICENSE`, `CONTRIBUTING.md`, `SECURITY.md`,
`GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, `MAINTAINERS.md`) are in place.

## Phase 1 — Foundation (current)

**Goal:** ship a genuinely useful Layer 1 OSS surface, build internal
Layer 2 tooling to deliver services, close three design partners
(see [spec 013](013-design-partner-model.md)).

### Layer 1 — ships publicly (Apache-2.0)

- **Fabric SDK (Python):**
  - `Fabric` client, `Decision` context manager
  - Inline guardrail chain (Presidio + NeMo rails)
  - Retrieval recording (spec 003)
  - Escalation pause primitive (spec 007)
  - Adapters: LangGraph, Microsoft Agent Framework, CrewAI
- **Guardrail sidecars:**
  - Presidio sidecar (UDS, default recognizers)
  - NeMo Guardrails sidecar (UDS, starter Colang rails)
- **OTel Collector distribution:**
  - Pre-configured with Fabric-standard processors
  - Redaction + sampling baked in
- **Reference agent:**
  - End-to-end example exercising guardrails + retrieval + escalation
- **Helm chart (Layer 1):**
  - Deploys SDK sidecars + OTel Collector within a tenant VPC

### Internal — SingleAxis builds but does not publish

Layer 2 tooling for pattern observation, rubric authoring, and managed
operations — used to deliver design partner engagements. See
[spec 012](012-oss-commercialization-strategy.md). Internal deliverables
include vertical playbooks (HIPAA, SR 11-7, EU AI Act high-risk) and
first-draft evidence artifacts.

### Exit criteria (Phase 1)

- Three design partners in Stage D of
  [spec 013](013-design-partner-model.md) (managed operation)
- At least one live agent per partner
- At least one internal compliance review per partner
- Layer 1 OSS stable enough for external adopters to install without
  hand-holding

### Out of scope in the public roadmap

The following exist or will exist as SingleAxis internal tooling or
future commercial products. They are **not** public Layer 1 deliverables:

- Evidence bundle generation
- Signed rubric library
- Compliance dashboards and auditor-grade UI
- Escalation service backing HITL workflows
- Cross-trace pattern detection
- Multi-cluster Control Plane
- Hosted evidence archive

## Phase 2 — Broaden

**Goal:** broaden Layer 1 surface; harden Layer 2 tooling internally;
begin packaging reusable patterns.

### Layer 1 additions

- **Additional SDK languages:** Go, TypeScript (same sidecar model;
  gRPC or UDS bridge)
- **Rails library:** broader NeMo Colang rail catalog, organized by
  regulatory profile
- **OTel processor library:** community-contributable Fabric-standard
  processors for emerging regulatory needs
- **Conformance tests:** a test suite tenants run to verify their
  installation produces Fabric-compliant spans
- **More reference agents:** one per covered vertical (healthcare,
  finance, support)

### Internal (Layer 2)

- ***REDACTED***
  signal
- Managed-service tooling hardens
- First drafts of evidence artifacts written by hand against real
  audits — precursor to Layer 3 productization

### Entry / Exit

- **Entry:** ***REDACTED*** reaches Stage D.
- **Exit:** ten customers in managed operation; pattern library stable
  enough to productize in Phase 3.

## Phase 3 — Stability and GA

**Goal:** ***REDACTED*** into a commercial offering; Layer 3
ships as a named product.

### Layer 1 additions

- **API stability commitments** (SDK, OTel attribute wire schema)
- **Long-term support branches** for Layer 1 minor releases
- **Expanded adapter surface** as new orchestration frameworks emerge
- **First-class OpenShift, GKE, EKS recipes**

### Layer 2 productization (not public)

Managed-service offering formalizes (pricing, SLA, onboarding). Rubric
library ships as a commercial product under a proprietary license.

### Layer 3 productization (not public)

Evidence bundle generator, compliance dashboards, reviewer workflows,
hosted archive, and SASF attestation infrastructure ship as commercial
products.

### Entry / Exit

- **Entry:** ten customers in managed operation.
- **Exit:** Layer 3 product in use by at least ***REDACTED***;
  external security audit renewed.

## Phase 4 — Layer 1 general availability

**Goal:** a stable, widely-deployed Layer 1 substrate.

**Includes:**

- API stability commitments on SDK, wire schema, Helm chart values
- Published conformance tests
- Documented upgrade paths from earlier versions
- Full SRE runbooks for Layer 1 operations
- Certified partnerships with upstream components (Presidio, NeMo,
  OpenTelemetry)

**Exit criteria:**

- Used in production by at least five tenants in regulated sectors
  running Layer 1 standalone (not as part of a services engagement)
- A recognised regulator or auditor cites Fabric or SASF attestation
  by name in published guidance
- Project governance moves toward (optional) foundation neutrality if
  warranted

## Risk register

| Risk | Mitigation |
|------|------------|
| Regulation changes faster than we can ship | Signed rubric channel (Layer 2, internal) lets SingleAxis push policy updates to customers without a full chart release. Not public. |
| A foundational dependency (Presidio, NeMo, LangGraph) goes in a hostile direction | Adapter layer in the SDK isolates upstream changes; components are swappable without breaking Fabric wire contracts. |
| Pattern-sharing clause pushes design partners away | Dry-run mode + explicit contract terms + anonymization policy give security review everything it needs ([spec 013](013-design-partner-model.md)). |
| Open-source contribution flow fails to materialise | Deep integration partnerships (OpenTelemetry, Presidio, NeMo) substitute for community-maintainer model. |
| Competitive closed platforms commoditise "compliance" framing | Fabric's wedge is open Layer 1 + proprietary Layer 2/3 + attestation network. A closed platform cannot credibly offer the OSS substrate; an open-source competitor cannot offer the attestation. |
| SingleAxis services capacity bottlenecks Phase 1 | ***REDACTED*** concurrently ([spec 013](013-design-partner-model.md)). Don't oversell. |
| Publishing Layer 2/3 roadmap signals to competitors | This spec intentionally does not detail Layer 2/3 plans. Those live in SingleAxis-internal documentation. |

## References

- [012 — OSS Distribution & Commercialization Strategy](012-oss-commercialization-strategy.md)
- [013 — Design Partner Engagement Model](013-design-partner-model.md)
- [001 — Product Vision & Positioning](001-product-vision.md)
- [000 — Overview & Conventions](000-overview.md)
