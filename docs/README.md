# docs/

User-facing documentation for Fabric. The authoritative design of
record lives in [`../specs/`](../specs/); these pages are the
shorter, action-oriented doors in.

## Start here

- [**Quickstart**](quickstart.md) — 5-minute walkthrough: install
  the SDK, wrap one agent turn, see telemetry.
- [**Architecture**](architecture.md) — the 3-layer mental model
  (SDK / sidecars / collector) and the "never block the agent
  request path" principle. Links to the authoritative specs.
- [**Product planes and packaging**](../specs/025-product-planes-and-packaging.md)
  — the canonical Connect, Control, Observe, Assurance, Governance,
  and Management model, including lifecycle and deployment options.
- [**OpenTelemetry GenAI conventions**](genai-semantic-conventions.md) —
  standard spans, attributes, metrics, compatibility, and privacy controls.
- [**Integration models**](integration-models.md) — select and compose SDK,
  framework, gateway, receiver, vendor, and eBPF discovery integrations without
  overstating their visibility or control.
- [**Connect capability contract**](../contracts/connect/v1/README.md) —
  digest-pinned machine-readable connector claims, evidence, and blind spots.
- [**Assurance findings**](assurance-findings.md) — one trace-correlated result
  contract for deterministic tests, judges, red teams, and human review, with
  clear Observe and Governance boundaries.
- [**Deployment**](deployment.md) — Helm chart, the
  `permissive-dev` and `eu-ai-act-high-risk` profiles, and the
  L1-OSS / L2-control-plane boundary.
- [**Building Fabric**](building-fabric.md) — engineering rules for
  rebuilding Fabric as operational infrastructure for autonomous
  systems.
- [**Install a pinned release**](install.md) — production installation,
  artifact pinning, promotion records, and deployment constraints.
- [**Verify a release**](verify-release.md) — hashes, signatures,
  provenance, and exact-SHA qualification evidence.
- [**Decision Graph**](decision-graph.md) — the causal graph primitive
  that powers replay, audit, governance, and evaluation.
- [**OSS / Commercial Boundary**](oss-commercial-boundary.md) — what
  stays open, what moves to the commercial repo, and why.

## Reference surfaces

- [**Operations — Disaster recovery**](operations/dr.md) — DR
  posture and pointers to the chart + bootstrap Job.

Compliance control mappings (Fabric artifact → regulatory control)
are roadmap; the structure is captured in
[`specs/009-compliance-mapping.md`](../specs/009-compliance-mapping.md).

## Status

Beta — v0.7.x. The docs cover the surfaces the OSS code ships today.
Anything marked "Roadmap / not yet shipping" in the spec or
component README is called out explicitly in the docs too — we'd
rather under-document than overclaim.

Longer-form reference material (component READMEs, SDK API docs)
still lives alongside the code in [`../components/`](../components/)
and [`../sdk/`](../sdk/). A generated static site (MkDocs + Material)
is a Phase 2 deliverable; until then, browse the Markdown directly
on GitHub.
