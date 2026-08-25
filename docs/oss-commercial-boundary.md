# Public distribution boundary

Fabric keeps the reconstruction path open and backend-neutral:

- **Public OSS:** Connect, Observe, Relay, local Control building blocks,
  Assurance contracts/runners, conformance, and deployment packaging.
- **External implementations:** customer-owned or SingleAxis-provided
  Management, Governance, retained evidence, and managed Assurance services.

## Public repo

The public `singleaxis-fabric` repo should contain:

| Area | Examples |
|---|---|
| SDKs | Python, TypeScript, future Go/Java clients |
| Framework adapters | LangGraph, CrewAI, OpenAI Agents SDK, Microsoft Agent Framework |
| OpenTelemetry contracts | `fabric.*` attributes, GenAI mappings, span/event schemas |
| Collector processors | allowlist, redaction, routing, sampling, policy hooks |
| Guardrail sidecars | Presidio, NeMo clients and packaging |
| Local red-team wrapper | Garak, PyRIT, Promptfoo runner normalization |
| Helm chart | collector/relay, sidecars, local observability, profiles |
| Reference agents | smoke agents and examples |
| Conformance tests | fixtures that validate emitted telemetry |
| Public specs | architecture, schemas, deployment posture |

## Capabilities outside the OSS runtime

The following may be implemented by a customer backend or by the SingleAxis
Platform. None is a hidden dependency of public capture or export:

| Area | Examples |
|---|---|
| Decision Graph engine | graph builder, stores, query APIs, replay indexes |
| Replay orchestration | checkpoint coordination, side-effect suppression |
| Runtime evals | judge workers, rubric routing, drift analysis |
| Governance control plane | policy history, approvals, org-wide controls |
| Evidence | signed bundles, retention jobs, compliance mappings |
| HITL workflows | reviewer queues, signed verdicts, SLAs |
| Enterprise UI | admin, audit, evidence, reviewer surfaces |
| Enterprise integrations | SIEM, GRC, ticketing, SSO/SCIM, WORM storage |

## Boundary rules

1. Public code must not import from private code.
2. Public docs may describe commercial behavior, but must label it as
   commercial or roadmap.
3. Commercial egress must be opt-in and inspectable.
4. The public capture path must never phone home.
5. Public schemas must be stable enough for third-party tooling.
6. Commercial code should not live under `_internal/` in the public repo.
7. Compliance claims must say "technical evidence" unless certification
   is actually provided.

## Repository rule

This repository carries only public contracts, OSS implementations, examples,
and release qualification. Private implementation topology, credentials,
customer overlays, commercial planning, and unpublished roadmap detail must
not be committed here. Public docs may describe compatible platform behavior
only to explain an integration boundary or an explicitly labeled capability.
