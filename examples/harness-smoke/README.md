# harness-smoke

Minimal Fabric SDK program that exercises the integration harness end
to end — one happy-path decision and one jailbreak attempt — so you
can verify the stack is live before pointing a real product at it.

## Run

```bash
# 1. start the harness
cd deploy/compose
make up

# 2. install deps and run smoke
cd ../../examples/harness-smoke
uv venv
source .venv/bin/activate
uv pip install -e ../../sdk/python \
  opentelemetry-sdk \
  opentelemetry-exporter-otlp-proto-http
python smoke.py
```

Expected output:

```
happy-path final output: Your balance is $0.00. Email: [REDACTED_EMAIL].
jailbreak blocked: rail=jailbreak_defence action=refuse
done — check http://localhost:3000 for the two traces
```

Open Langfuse (admin@fabric.local / fabric-admin) and you should see
two `fabric.decision` traces under the `fabric-harness` project.
