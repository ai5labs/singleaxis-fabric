# SingleAxis Fabric recorder SDK for TypeScript

`@singleaxis/fabric` adds passive OpenTelemetry activity capture to Node.js AI
agents. It records identity and causal context for model calls, tool calls,
retrieval, memory activity, side effects, delegation, retries and failures. It
does not block, alter or delay the agent.

```text
CAPTURE -> PROTECT -> DELIVER
   ^
   |
TypeScript SDK (optional instrumentation)
```

Protection and delivery happen in the customer-controlled Fabric Node. Raw
prompt, response, tool argument and tool result fields are not part of the
recorder SDK's default activity surface.

## Install

```bash
npm install @singleaxis/fabric @opentelemetry/api
```

## Capture one agent decision

```ts
import { Fabric } from "@singleaxis/fabric";

const fabric = new Fabric({ tenantId: "acme", agentId: "support-agent" });

await fabric.decision({ sessionId: "session-42", requestId: "request-7" }, async (decision) => {
  await decision.llmCall(
    { provider: "provider-example", operationName: "chat", model: "model-example" },
    async (call) => {
      // Invoke the customer's model client here.
      call.setResponse({ model: "model-example" });
    },
  );

  await decision.toolCall("lookup_order", { callId: "tool-1" }, async (tool) => {
    tool.setArguments('{"order_id":"order-123"}');
    // Invoke the customer's tool here.
    tool.setResult('{"status":"shipped"}');
  });
});
```

Tool payload setters record hashes, not the raw payload. The SDK emits through
the OpenTelemetry provider configured by the host application.

Use `fabric.execution(...)` to correlate multiple decisions. `Decision` also
captures retrieval, memory, side effects, checkpoints, delegation, MCP
inventory, skills, hooks, file access and generic interactions.

## Stable recorder-v1 surface

The package root exports capture/correlation types and an allowlisted
`attributes` namespace. It does not export judges, evaluation queues,
guardrails, prompt-time PII/NeMo controls, policy engines, tool authorization or
escalation enforcement.

Recorder-v1 artifacts do not contain the former runtime-control, evaluation,
policy, authorization or escalation methods or attribute families. This is an
intentional breaking boundary so installing the recorder cannot alter an
agent's request path.

See the
[recorder-v1 spec](https://github.com/singleaxis/singleaxis-fabric/blob/main/specs/027-recorder-v1.md)
for the authoritative product boundary.

## Development

```bash
npm ci
npm run lint
npm run typecheck
npm test
npm run build
npm run test:package
```
