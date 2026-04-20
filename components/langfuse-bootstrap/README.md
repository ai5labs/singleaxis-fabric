# langfuse-bootstrap

One-shot Job that turns a vanilla Langfuse install into *the* Fabric
UI by applying Fabric's curated configuration (score configs, prompt
presets, and saved-view URLs) via the Langfuse public API.

This is the Layer 1 embodiment of Fabric's UI strategy: reuse
Langfuse — don't build a custom console — and ship a declarative
bundle that makes it Fabric-aware on first install.

## What it applies

| Kind | Source |
|---|---|
| Score configs | `curated/common.yaml` (baseline rubric IDs from spec 006 §3) |
| Prompt presets | `curated/common.yaml` (e.g. escalation-triage checklist) |
| Saved-view URLs | `curated/common.yaml` + per-profile overlay |
| Per-profile additions | `curated/<profile>.yaml` (e.g. `eu-ai-act-high-risk.yaml`) |

Idempotent: rerunning the Job against an already-configured Langfuse
is a no-op, so tenants can redeploy freely.

## Run locally

```bash
cd components/langfuse-bootstrap
uv venv && source .venv/bin/activate
uv pip install -e .
fabric-langfuse-bootstrap run \
  --host http://localhost:3000 \
  --public-key pk-lf-harness \
  --secret-key sk-lf-harness \
  --profile permissive-dev \
  --curated-dir ./curated
```

For the compose harness this is exposed as `make up-bootstrap`.

## Run in Kubernetes

Shipped as a post-install Job in `charts/fabric/charts/langfuse/`.
Enable via chart values:

```yaml
langfuse:
  bootstrap:
    enabled: true
    profile: eu-ai-act-high-risk    # defaults to permissive-dev
```

## Curated bundle shape

See `src/fabric_langfuse_bootstrap/config.py` for the pydantic models;
the on-disk YAML is a direct one-to-one mapping. Override behavior:
`common.yaml` + `<profile>.yaml` are merged by `name` so overlays are
additive, not replacements.

## Why this lives in Layer 1

The Fabric UI strategy (memory: reuse Langfuse, don't build our own)
makes curated config the single piece of the UI layer that Fabric
owns. Keeping it declarative (YAML → API calls) means swapping the
tracing UI later is a bundle rewrite, not a rebuild.
