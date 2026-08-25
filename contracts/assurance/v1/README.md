# Fabric AssuranceFinding contract v1

`AssuranceFinding` is the public, implementation-neutral result envelope for
deterministic tests, LLM judges, red-team exercises, and human review. It
records a versioned conclusion and references its evidence without embedding
prompts, completions, transcripts, or other raw subject content.

## Contents

- [`manifest.json`](manifest.json) pins the schema and every positive and
  negative fixture by SHA-256.
- [`schema/assurance-finding-v1.schema.json`](schema/assurance-finding-v1.schema.json)
  is the strict JSON Schema.
- [`fixtures/valid/`](fixtures/valid/) covers pre-deployment deterministic and
  red-team results, continuous LLM-judge findings, incident review, appeal,
  and supersession.
- [`fixtures/invalid/`](fixtures/invalid/) locks rejection of missing runtime
  correlation, raw prompt fields, false confidence, missing model provenance,
  invalid chronology, and self-supersession.

## Identity and lifecycle rules

`finding_id` remains stable as a conclusion moves through review;
`record_version` increments whenever its retained status representation
changes. `run_id` correlates findings produced by one test, judge, red-team, or
review run. Governance storage must retain every received record version rather
than overwrite history. `status_changed_at` distinguishes original finding
finalization from a later appeal or supersession transition.

Correlation is conditional rather than invented:

- `pre_deployment` requires an agent and subject digest, but deployment,
  execution, decision, trace, and span IDs may be `null`.
- `runtime_continuous` requires a deployment plus at least an execution,
  decision, or trace ID.
- `incident` requires a deployment, incident ID, and at least an execution,
  decision, or trace ID.
- A span ID is valid only with its trace ID.

Source-specific schema conditions require a suite version for deterministic
tests and red teams, rubric and model provenance for LLM judges, and policy
version plus a reviewer principal reference for human review. Model-executed
red teams must also carry model provenance.

## Content boundary

The subject and each evidence item contain a SHA-256 digest and optional
resolvable reference. There are no fields for prompt, completion, transcript,
tool payload, review notes, or free-form finding text. Sensitive material stays
in an access-controlled evidence store; the finding remains suitable for the
trace and governance paths.

## Validate

From the repository root:

```bash
python scripts/contracts/validate_assurance_contract.py
```

The validator checks schema formats and conditions, timestamps, confidence
semantics, outcome/severity consistency, evaluator provenance, evidence ID
uniqueness, appeal chronology, supersession, pinned digests, and complete JSON
artifact coverage.

Changing a schema field, fixture, semantic invariant, or digest is a public
contract change. Execution workers, model invocation, red-team orchestration,
review queues, and persistence services are intentionally outside this
contract.
