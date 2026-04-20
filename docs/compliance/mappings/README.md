# Compliance control mappings

Per-regulation control mappings that tie Fabric artifacts
(decision spans, guardrail events, judge scores, escalation records)
to specific regulatory controls.

## Status

**In progress.** No authoritative mapping files land here yet.
Faking mappings before the underlying evidence is reliable would
mislead auditors and tenants; we'd rather ship late than ship
wrong.

The design of record and roadmap for these mappings live in
[`specs/009-compliance-mapping.md`](../../../specs/009-compliance-mapping.md),
including the structure each mapping entry follows (Fabric artifact
→ evidence form → surfacing in the evidence bundle → explicit gaps).

## Planned first mappings

- EU AI Act (Articles 9, 10, 12, 13, 14, 15, 17, 61)
- NIST AI RMF 1.0
- ISO/IEC 42001

Each will land as a separate Markdown file with a machine-readable
YAML companion so mapping tables can be regenerated automatically.
See spec 009 for the control structure.

## Why these aren't here yet

Honest mappings require the underlying evidence pipeline (Context
Graph queries, signed attestations, judge score history) to be
stable enough that the mapping describes what actually happens —
not what we intend to happen. In Phase 1a, the evidence pipeline is
narrow (decision spans + guardrail events + escalation records);
broad regulation-to-control mappings would overclaim.
