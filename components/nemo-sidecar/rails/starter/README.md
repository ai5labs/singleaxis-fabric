# Starter rails bundle

Minimal Colang rails shipped with `fabric-nemo-sidecar` as a default.
The bundle is deterministic (no LLM required) and covers the single
most common prompt-injection class — instruction-override attempts —
so fresh installs are not shipped fail-open.

## Files

- `config.yml` — NeMo Guardrails engine config. `models: []` so the
  bundle loads without credentials.
- `rails.co` — Colang flows. Currently: `jailbreak defence`.

## Helm wiring

The `nemo-sidecar` Helm subchart ships this bundle as a built-in
ConfigMap when `starterRails.enabled=true` (the default). Override
with your own `railsConfigMap.name` once you have a production
bundle. See `charts/fabric/charts/nemo-sidecar/values.yaml`.

## Extending

Production rails should layer on top of the starter, not replace it.
The recommended next rails are:

1. `self check input` — LLM-graded jailbreak detection (requires
   `models:` entry).
2. `mask sensitive data` — PII redaction via regex or Presidio.
3. `off topic` — domain-bounded output-stage check.

Keep flow names stable: they surface on decision spans as the
`rail` attribute and are referenced by judge rubrics and dashboards.
