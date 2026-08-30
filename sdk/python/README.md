# SingleAxis Fabric recorder SDK for Python

The Python SDK adds passive OpenTelemetry activity capture to an AI agent. It
records enough identity and causal context to reconstruct model calls, tool
calls, retrieval, memory activity, side effects, delegation, retries and
failures. It does not block or alter the agent.

```text
CAPTURE -> PROTECT -> DELIVER
   ^
   |
Python SDK (optional instrumentation)
```

Protection and delivery happen in the customer-controlled Fabric Node. Raw LLM
content capture is disabled by default.

## Install

```bash
pip install singleaxis-fabric
```

Add OTLP export and only the integrations you use:

```bash
pip install "singleaxis-fabric[otlp,openai]"
```

## Capture one agent decision

```python
from fabric import Fabric, FabricConfig

fabric = Fabric(FabricConfig(tenant_id="acme", agent_id="support-agent"))

with fabric.decision(session_id="session-42", request_id="request-7") as decision:
    with decision.llm_call(provider="provider-example", model="model-example") as call:
        # Invoke the customer's model client here.
        call.set_response(model="model-example")

    with decision.tool_call("lookup_order", call_id="tool-1") as tool:
        tool.set_arguments('{"order_id":"order-123"}')
        # Invoke the customer's tool here.
        tool.set_result('{"status":"shipped"}')
```

The SDK hashes tool payloads locally. It does not send telemetry by itself;
configure the OpenTelemetry provider/exporter used by the host application to
send to Fabric Node.

Use `Fabric.execution(...)` to correlate multiple decisions and the retrieval,
memory, side-effect, checkpoint, delegation and generic-interaction methods on
`Decision` to capture consequential activity beyond LLM and tool calls.

## Stable recorder-v1 surface

The `fabric` package root exports capture, activity-correlation, governed
content-reference and integrity primitives only. Framework and provider
instrumentation remain optional extras.

It does not export or install judges, evaluation workers, guardrail engines,
prompt-time PII/NeMo controls, policy engines, tool authorization, escalation
enforcement or management-plane commands.

Recorder-v1 artifacts do not contain the former runtime-control, judge,
evaluation, policy, authorization or escalation modules. Their former
`Decision` methods and `Fabric` constructor parameters have also been removed;
this is an intentional breaking boundary so installing the recorder cannot put
enforcement behavior in an agent's request path.

See the
[SDK scope](https://github.com/singleaxis/singleaxis-fabric/blob/main/sdk/python/SCOPE.md)
and [recorder-v1 spec](https://github.com/singleaxis/singleaxis-fabric/blob/main/specs/027-recorder-v1.md)
for the authoritative boundary.

## Development

```bash
uv sync --all-extras --dev
uv run pytest
uv run ruff check src tests
uv run mypy src
```
