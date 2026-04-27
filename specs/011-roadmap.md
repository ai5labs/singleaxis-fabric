---
title: Phased Execution Roadmap
status: draft
revision: 3
last_updated: 2026-04-27
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

## Phase 0 — Scaffolding & specs (complete)

**Goal:** publish the design of record and the repo structure.

**Status:** complete. The specs directory is at revision 1+ and the
root governance files (`LICENSE`, `CONTRIBUTING.md`, `SECURITY.md`,
`GOVERNANCE.md`, `CODE_OF_CONDUCT.md`, `MAINTAINERS.md`) are in
place.

## Phase 1 — Foundation (current)

**Goal:** ship a genuinely useful OSS surface that an enterprise
platform team can install and operate without hand-holding.

### Public deliverables (Apache-2.0)

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
  - End-to-end example exercising guardrails + retrieval +
    escalation
- **Helm chart:**
  - Deploys SDK sidecars + OTel Collector within a tenant VPC
  - Two regulatory profiles: `permissive-dev` and
    `eu-ai-act-high-risk`

### Exit criteria (Phase 1)

- Public OSS stable enough for external adopters to install without
  hand-holding (the quickstart works end-to-end on a fresh checkout)
- Inline guardrail latencies meet the published P99 budgets in
  spec 005 under representative load
- Reference agent passes the documented decision-span contract
- Released artifacts are signed (cosign keyless) and accompanied by
  SBOMs (CycloneDX + SPDX)

## Phase 2 — Broaden the surface

**Goal:** broaden language support and the policy-rail catalog so
Fabric is reachable from non-Python orchestration stacks and ships
useful starter rails for additional regulations.

### Public additions

- **Additional SDK languages:** Go, TypeScript (same sidecar model;
  gRPC or UDS bridge)
- **Rails library:** broader NeMo Colang rail catalog, organized by
  regulatory profile
- **OTel processor library:** community-contributable
  Fabric-standard processors for emerging regulatory needs
- **Conformance tests:** a test suite tenants run to verify their
  installation produces Fabric-compliant spans
- **More reference agents:** one per covered vertical (healthcare,
  finance, support)

### Entry / Exit

- **Entry:** Phase 1 exit criteria all met.
- **Exit:** at least three independent organizations have published
  Fabric-instrumented agents (public references, conference talks,
  blog posts), and the conformance test suite is exercised by CI
  against multi-language SDK builds.

## Phase 3 — Stability & general availability

**Goal:** a stable, widely-deployed substrate with API commitments
that production users can pin against.

### Public additions

- **API stability commitments** (SDK, OTel attribute wire schema)
- **Long-term support branches** for minor releases
- **Expanded adapter surface** as new orchestration frameworks
  emerge
- **First-class OpenShift, GKE, EKS recipes**

### Exit criteria

- Used in production by at least five tenants in regulated sectors
  running the OSS standalone
- A recognised regulator or auditor cites Fabric by name in
  published guidance
- Documented upgrade paths from earlier versions
- Full SRE runbooks for operations
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

## References

- [001 — Product Vision & Positioning](001-product-vision.md)
- [000 — Overview & Conventions](000-overview.md)
