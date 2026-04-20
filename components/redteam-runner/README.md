# redteam-runner

Scheduled adversarial testing against a tenant's agent endpoint. Wraps
[Garak](https://github.com/leondz/garak) and
[PyRIT](https://github.com/Azure/PyRIT) behind a single CLI + CronJob
and emits OTel spans the Fabric collector recognizes (`event_class =
redteam_run | redteam_probe`).

## Why a wrapper

Garak and PyRIT are the two most useful open adversarial testing libs
today, but neither is auditor-ready on its own:

- their verdicts are scored on different scales,
- neither emits structured telemetry,
- and both are still churning on minor versions.

This component gives operators **one** contract (OTel spans + a single
result schema) regardless of which suite ran. Audit evidence for
"agent X was tested for jailbreak resistance weekly" becomes a simple
trace query.

## Install

Pulls Garak/PyRIT only when you ask for them — they're heavyweight and
only needed at runtime.

```bash
pip install .            # bare adapter; drivers short-circuit to ERROR
pip install '.[suites]'  # with Garak + PyRIT
```

## CLI

```bash
fabric-redteam-runner \
    --config   /etc/fabric/redteam/run.yaml \
    --otlp-endpoint http://otel-collector:4318
```

Exit codes:

- `0` — all probes passed (or errored because a library was missing)
- `2` — at least one probe had verdict `FAIL` and `--fail-on-findings`
  is on (default)

Passing `--fail-on-findings=false` decouples the CronJob's success
signal from the findings, useful when you want the run to complete
and downstream dashboards to drive alerting.

## Run config

Minimal example:

```yaml
tenant_id: acme-prod
agent_id: support-bot
profile: eu-ai-act-high-risk
target:
  url: https://support-bot.acme.example.com/respond
  headers:
    Authorization: Bearer ${env:AGENT_BEARER}
  timeout_seconds: 30
suites:
  - name: garak
    probes:
      - promptinject.HijackHateHumans
      - continuation.ContinueSlursReclaimedSlurs80
    attempts_per_probe: 1
  - name: pyrit
    scenarios:
      - jailbreak_fuzzer
      - prompt_injection
```

`${env:NAME}` placeholders resolve from the process environment so
bearer tokens don't sit in your ConfigMap.

## Emitted telemetry

Per run: one `fabric.redteam.run` parent span + one
`fabric.redteam.probe` child per probe.

Attributes the Fabric collector's allowlist recognizes:

| attribute | example |
|---|---|
| `event_class` | `redteam_run`, `redteam_probe` |
| `fabric.redteam.run_id` | `run-7b2a…` |
| `fabric.tenant_id` | `acme-prod` |
| `fabric.agent_id` | `support-bot` |
| `fabric.redteam.suite` | `garak` |
| `fabric.redteam.probe` | `promptinject.HijackHateHumans` |
| `fabric.redteam.verdict` | `pass` / `fail` / `error` |
| `fabric.redteam.findings` | integer count |

Prompt and response bodies are **not** exported — only a blake2b-16
hash so auditors can correlate attempts across runs without the
runner becoming a data-exfiltration path.

## Deployment

- Chart: [`charts/fabric/charts/redteam-runner`](../../charts/fabric/charts/redteam-runner)
- Image: `fabric/redteam-runner:<version>-suites` for production

The chart renders a CronJob with:

- the run config mounted from a ConfigMap at
  `/etc/fabric/redteam/run.yaml`
- bearer / API keys injected from a Secret via
  `extraEnv` + the `${env:...}` placeholder
- OTel endpoint pointed at the in-cluster collector
- non-root, read-only root filesystem, dropped capabilities

## Limits

- Each probe is run serially; parallelism is future work. Garak's own
  parallelism is unreliable across versions, and PyRIT scenarios can
  share state — serial runs are currently the safe default.
- Partial-failure surfacing is best-effort: if Garak's harness crashes
  mid-probe, the probe is recorded as `ERROR` with the exception note,
  not broken down per attempt.

## Authoritative specs

- [`../../specs/002-architecture.md`](../../specs/002-architecture.md) (L4 — detect)
- [`../../specs/009-compliance-mapping.md`](../../specs/009-compliance-mapping.md) (evidence mapping)

## License

Apache-2.0.
