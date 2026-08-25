// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

/**
 * Conformance test — proves the TS SDK emits the SAME wire contract as the
 * Python SDK by deep-equal-asserting normalized TS spans against the SAME
 * public activity-contract fixtures the Python conformance suite uses
 * (`../../../contracts/activity/v1`). The SDKs consume one neutral contract;
 * neither language implementation owns it.
 *
 * Covered (core capture) scenarios: `bare_decision`, `llm_call`,
 * `tool_call`. The fixed conformance identifiers mirror Python's
 * `scenarios.py` so the emitted spans land verbatim against the goldens.
 */

import { createHash } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

import { trace } from "@opentelemetry/api";
import {
  BasicTracerProvider,
  InMemorySpanExporter,
  SimpleSpanProcessor,
  type ReadableSpan,
} from "@opentelemetry/sdk-trace-node";
import { afterAll, beforeAll, beforeEach, describe, expect, it } from "vitest";

import { Fabric, resetCoverageRegistry, sha256Hex } from "../src/index.js";
import { normalizeSpans } from "./normalize.js";

// Fixed per-turn identifiers — mirror sdk/python/tests/conformance/scenarios.py.
const TENANT_ID = "tenant-conformance";
const AGENT_ID = "agent-conformance";
const PROFILE = "permissive-dev";
const SESSION_ID = "session-0001";
const REQUEST_ID = "request-0001";
const USER_ID = "user-0001";

// Fixed execution-correlation ids + attempt metadata — mirror scenarios.py.
// Supplied verbatim and NOT normalized away, so the golden asserts the literal
// value stamped on the execution span and inherited by the inner decision.
const EXECUTION_ID = "execution-0001";
const WORKFLOW_ID = "workflow-0001";
const EXECUTION_ATTEMPT_ID = "attempt-0001";
const EXECUTION_ATTEMPT = 1;

const HERE = dirname(fileURLToPath(import.meta.url));
const CONTRACT_DIR = resolve(HERE, "..", "..", "..", "contracts", "activity", "v1");
const MANIFEST_PATH = resolve(CONTRACT_DIR, "manifest.json");

type ScenarioManifest = {
  name: string;
  fixture: string;
  sha256: string;
  support: string[];
};

type ContractManifest = {
  contract: string;
  version: string;
  schema: { path: string; sha256: string };
  scenarios: ScenarioManifest[];
};

const MANIFEST = JSON.parse(readFileSync(MANIFEST_PATH, "utf-8")) as ContractManifest;
const TYPESCRIPT_SCENARIOS = MANIFEST.scenarios
  .filter((scenario) => scenario.support.includes("typescript"))
  .map((scenario) => scenario.name)
  .sort();
const EXECUTED_SCENARIOS = new Set<string>();

function sha256File(path: string): string {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

function loadGolden(name: string): unknown {
  const scenario = MANIFEST.scenarios.find((entry) => entry.name === name);
  if (!scenario || !scenario.support.includes("typescript")) {
    throw new Error(`scenario ${JSON.stringify(name)} is not declared for TypeScript`);
  }
  EXECUTED_SCENARIOS.add(name);
  return JSON.parse(readFileSync(resolve(CONTRACT_DIR, scenario.fixture), "utf-8"));
}

const exporter = new InMemorySpanExporter();
let provider: BasicTracerProvider;

beforeAll(() => {
  provider = new BasicTracerProvider({
    spanProcessors: [new SimpleSpanProcessor(exporter)],
  });
  trace.setGlobalTracerProvider(provider);
});

afterAll(async () => {
  await provider.shutdown();
  trace.disable();
  expect([...EXECUTED_SCENARIOS].sort()).toEqual(TYPESCRIPT_SCENARIOS);
});

beforeEach(() => {
  exporter.reset();
});

function fabric(): Fabric {
  return new Fabric({ tenantId: TENANT_ID, agentId: AGENT_ID, profile: PROFILE });
}

function decision<T>(f: Fabric, fn: (d: import("../src/index.js").Decision) => T): T {
  return f.decision({ sessionId: SESSION_ID, requestId: REQUEST_ID, userId: USER_ID }, fn);
}

function captured(): ReadableSpan[] {
  return [...exporter.getFinishedSpans()];
}

describe("conformance against shared Python goldens", () => {
  it("verifies the complete public contract manifest and artifact digests", () => {
    expect(MANIFEST.contract).toBe("singleaxis.fabric.activity");
    expect(MANIFEST.version).toBe("1.0.0");

    const names = MANIFEST.scenarios.map((scenario) => scenario.name);
    const fixtures = MANIFEST.scenarios.map((scenario) => scenario.fixture);
    expect(new Set(names).size).toBe(names.length);
    expect(new Set(fixtures).size).toBe(fixtures.length);

    const onDisk = readdirSync(resolve(CONTRACT_DIR, "goldens"))
      .filter((f) => f.endsWith(".json"))
      .map((f) => `goldens/${f}`)
      .sort();
    expect(onDisk).toEqual([...fixtures].sort());

    expect(sha256File(resolve(CONTRACT_DIR, MANIFEST.schema.path))).toBe(MANIFEST.schema.sha256);
    for (const scenario of MANIFEST.scenarios) {
      expect(new Set(scenario.support).size).toBe(scenario.support.length);
      expect(scenario.support.length).toBeGreaterThan(0);
      expect(scenario.support.every((item) => ["python", "typescript"].includes(item))).toBe(true);
      expect(sha256File(resolve(CONTRACT_DIR, scenario.fixture))).toBe(scenario.sha256);
    }
  });

  it("bare_decision", () => {
    const f = fabric();
    decision(f, () => {
      // empty body — the bare decision span
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("bare_decision"));
  });

  it("execution", () => {
    const f = fabric();
    f.execution(
      {
        executionId: EXECUTION_ID,
        workflowId: WORKFLOW_ID,
        executionAttemptId: EXECUTION_ATTEMPT_ID,
        executionAttempt: EXECUTION_ATTEMPT,
      },
      () => {
        // A bare decision inside the execution: it inherits execution_id +
        // workflow_id + attempt metadata from the active execution (ALS).
        decision(f, () => {
          // empty body — the inherited decision span
        });
      },
    );
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("execution"));
  });

  it("llm_call", () => {
    const f = fabric();
    decision(f, (d) => {
      d.llmCall(
        {
          system: "anthropic",
          model: "claude-opus-4-8",
          temperature: 0.2,
          topP: 0.9,
          maxTokens: 512,
        },
        (call) => {
          call.setResponseModel("claude-opus-4-8");
          call.setUsage({ inputTokens: 120, outputTokens: 64, finishReason: "end_turn" });
        },
      );
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("llm_call"));
  });

  it("tool_call", () => {
    const f = fabric();
    decision(f, (d) => {
      d.toolCall("vector_search", { callId: "call-1" }, (tool) => {
        tool.setKind("retrieval");
        tool.setArguments('{"query":"refunds"}');
        tool.setResult('{"hits":3}');
        tool.setResultCount(3);
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("tool_call"));
  });

  it("guardrail_redaction", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordGuardrail({
        phase: "input",
        blocked: false,
        latencyMs: 4,
        policies: ["stub-redactor:pii"],
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("guardrail_redaction"));
  });

  it("guardrail_block", () => {
    const f = fabric();
    const result = {
      phase: "input" as const,
      blocked: true,
      latencyMs: 2,
      policies: ["stub-blocker:jailbreak"],
    };
    decision(f, (d) => {
      d.recordGuardrail(result);
      d.recordBlock(result);
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("guardrail_block"));
  });

  it("content_ref_stamped", () => {
    const f = fabric();
    decision(f, (d) => {
      // Mirrors the Python DeterministicContentStore: mem://<sha256(content)>.
      d.recordGuardrail({
        phase: "input",
        blocked: false,
        latencyMs: 4,
        policies: ["stub-redactor:pii"],
        contentRef: `mem://${sha256Hex("my email is alice@example.com")}`,
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("content_ref_stamped"));
  });

  it("escalation", () => {
    const f = fabric();
    decision(f, (d) => {
      d.requestEscalation({
        reason: "low confidence on refund eligibility",
        rubricId: "refund-eligibility-v1",
        triggeringScore: 0.42,
        mode: "async",
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("escalation"));
  });

  it("retrieval", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordRetrieval({
        source: "rag",
        query: "refund policy for late deliveries",
        resultCount: 2,
        resultHashes: ["a".repeat(64), "b".repeat(64)],
        sourceDocumentIds: ["doc-1", "doc-2"],
        latencyMs: 12,
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("retrieval"));
  });

  it("memory_read_write", () => {
    const f = fabric();
    decision(f, (d) => {
      d.remember({
        kind: "semantic",
        content: "customer prefers email contact",
        key: "pref:contact",
        tags: ["preference", "contact"],
        ttlSeconds: 86400,
      });
      d.recall({
        kind: "semantic",
        key: "pref:contact",
        content: "customer prefers email contact",
        source: "vector-store",
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("memory_read_write"));
  });

  it("side_effect", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordSideEffect({
        type: "ticket_create",
        targetSystem: "zendesk",
        operation: "create_ticket",
        requestPayload: '{"subject":"refund"}',
        resultPayload: '{"id":"T-100"}',
        idempotencyKey: "idem-100",
        approvalRequired: true,
        committed: true,
        rollbackSupported: false,
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("side_effect"));
  });

  it("checkpoint", () => {
    const f = fabric();
    decision(f, (d) => {
      d.checkpoint("after-retrieval", {
        stateHash: "c".repeat(64),
        checkpointId: "11111111-1111-1111-1111-111111111111",
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("checkpoint"));
  });

  it("eval_record", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordEval({
        rubricId: "faithfulness-v1",
        score: 0.91,
        dimension: "faithfulness",
        evaluatorName: "StubJudge:Faithfulness",
        evaluatorVersion: "1.2.0",
        confidence: 0.8,
        payloadRef: "tenant://payloads/req-0001",
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("eval_record"));
  });

  it("queue_judge", () => {
    const f = fabric();
    decision(f, (d) => {
      d.queueJudge({
        rubricId: "helpfulness-v1",
        dimensions: ["helpfulness", "tone"],
        payloadRef: "tenant://payloads/judge-0001",
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("queue_judge"));
  });

  it("policy_allow", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordPolicyEvaluation({
        engine: "stub-policy",
        policyId: "finance.refund.cap",
        decision: "allow",
        input: { amount: 50 },
        policyVersion: "v3",
        latencyMs: 1,
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("policy_allow"));
  });

  it("policy_deny", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordPolicyEvaluation({
        engine: "stub-policy",
        policyId: "finance.refund.cap",
        decision: "deny",
        input: { amount: 5000 },
        policyVersion: "v3",
        reason: "amount exceeds cap",
        evidenceRef: "tenant://evidence/deny-1",
        latencyMs: 1,
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("policy_deny"));
  });

  it("policy_fail_closed", () => {
    const f = fabric();
    decision(f, (d) => {
      // Mirrors the SDK fail-closed path: a raising engine becomes a deny
      // with a synthetic reason. No policy_version (the engine never returned).
      d.recordPolicyEvaluation({
        engine: "stub-policy-raising",
        policyId: "finance.refund.cap",
        decision: "deny",
        input: { amount: 50 },
        reason: "adapter raised: RuntimeError: engine unreachable",
        latencyMs: 1,
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("policy_fail_closed"));
  });

  it("tool_authorization_allow", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordToolAuthorization({
        toolName: "search_orders",
        decision: "allow",
        arguments: '{"order_id":"O-1"}',
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("tool_authorization_allow"));
  });

  it("tool_authorization_deny", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordToolAuthorization({
        toolName: "wire_transfer",
        decision: "deny",
        arguments: '{"amount":9999}',
        reason: "tool not on allow-list",
      });
    });
    const got = normalizeSpans(captured());
    expect(got).toEqual(loadGolden("tool_authorization_deny"));
  });

  it("decision_id_distinct", () => {
    const f = fabric();
    f.decision(
      {
        sessionId: SESSION_ID,
        requestId: REQUEST_ID,
        userId: USER_ID,
        decisionId: "decision-0001",
      },
      () => undefined,
    );
    expect(captured()[0]?.attributes["fabric.decision_id"]).toBe("decision-0001");
    expect(captured()[0]?.attributes["fabric.request_id"]).toBe(REQUEST_ID);
    expect(normalizeSpans(captured())).toEqual(loadGolden("decision_id_distinct"));
  });

  it("workflow_execution", () => {
    const f = new Fabric({
      tenantId: TENANT_ID,
      agentId: AGENT_ID,
      profile: PROFILE,
      workflowId: WORKFLOW_ID,
      executionId: EXECUTION_ID,
    });
    decision(f, () => undefined);
    expect(normalizeSpans(captured())).toEqual(loadGolden("workflow_execution"));
  });

  it("memory_erase", () => {
    const f = fabric();
    decision(f, (d) => {
      d.forget("semantic", "pref:contact");
      d.forget("semantic", "pref:contact", { tenantScope: true });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("memory_erase"));
  });

  it("memory_invalidate", () => {
    const f = fabric();
    decision(f, (d) => {
      d.remember({
        kind: "semantic",
        content: "customer prefers email contact",
        key: "pref:contact",
        invalidates: "prior:key",
      });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("memory_invalidate"));
  });

  for (const scenario of [
    ["policy_warn", "warn", 90, "amount near cap"],
    ["policy_escalate", "escalate", 500, "requires human approval"],
    ["policy_redact", "redact", 50, "strip PII from response"],
  ] as const) {
    it(scenario[0], () => {
      const f = fabric();
      decision(f, (d) => {
        d.recordPolicyEvaluation({
          engine: "stub-policy",
          policyId: "finance.refund.cap",
          decision: scenario[1],
          input: { amount: scenario[2] },
          policyVersion: "v3",
          reason: scenario[3],
          latencyMs: 1,
        });
      });
      expect(normalizeSpans(captured())).toEqual(loadGolden(scenario[0]));
    });
  }

  it("llm_call_rich", () => {
    const f = fabric();
    decision(f, (d) => {
      d.llmCall(
        {
          system: "anthropic",
          model: "claude-opus-4-8",
          temperature: 0.2,
          topP: 0.9,
          maxTokens: 512,
        },
        (call) => {
          call.setResponseModel("claude-opus-4-8");
          call.setUsage({ inputTokens: 120, outputTokens: 64, finishReason: "end_turn" });
          call.setCacheUsage({ cacheReadTokens: 1000, cacheCreationTokens: 200 });
          call.setStreaming({ ttftMs: 120, chunkCount: 42 });
          call.setRetry({ count: 1, reason: "rate_limit" });
        },
      );
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("llm_call_rich"));
  });

  it("tool_call_error", () => {
    const f = fabric();
    decision(f, (d) => {
      d.toolCall("charge_card", { callId: "call-1" }, (tool) => {
        tool.setKind("action");
        tool.recordError("timeout");
        tool.setRetry({ count: 2, reason: "timeout" });
        tool.setIdempotency({ idempotent: true, key: "idem-tool-1" });
      });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("tool_call_error"));
  });

  it("step_retry", () => {
    const f = fabric();
    decision(f, (d) => {
      d.toolCall(
        "vector_search",
        {
          callId: "call-1",
          stepId: "step-0001",
          stepAttemptId: "step-attempt-0002",
          stepAttempt: 2,
          stepRetryReason: "tool_timeout",
          stepRetryPreviousAttemptId: "step-attempt-0001",
        },
        (tool) => tool.setKind("retrieval"),
      );
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("step_retry"));
  });

  it("side_effect_parent_tool_call", () => {
    const f = fabric();
    decision(f, (d) => {
      d.toolCall("create_ticket", { callId: "call-1" }, (tool) => tool.setKind("action"));
      d.recordSideEffect({
        type: "ticket_create",
        targetSystem: "zendesk",
        operation: "create_ticket",
        parentToolCallId: "call-1",
        sideEffectId: "77777777-7777-7777-7777-777777777777",
      });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("side_effect_parent_tool_call"));
  });

  it("replay_metadata", () => {
    const f = fabric();
    f.decision(
      {
        sessionId: SESSION_ID,
        requestId: REQUEST_ID,
        userId: USER_ID,
        decisionId: "66666666-6666-6666-6666-666666666666",
      },
      (d) => {
        d.checkpoint("after-retrieval", {
          stateHash: "c".repeat(64),
          checkpointId: "44444444-4444-4444-4444-444444444444",
        });
        d.recordSideEffect({
          type: "ticket_create",
          targetSystem: "zendesk",
          operation: "create_ticket",
          replayBehavior: "suppress",
          sideEffectId: "55555555-5555-5555-5555-555555555555",
        });
        d.recordReplayMetadata({
          stateHash: "d".repeat(64),
          toolResultHashes: ["a".repeat(64), "b".repeat(64)],
        });
      },
    );
    expect(normalizeSpans(captured())).toEqual(loadGolden("replay_metadata"));
  });

  it("mcp_inventory", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordMcpInventory({
        server: "weather-mcp",
        transport: "stdio",
        tools: [
          {
            name: "get_weather",
            description: "Return the current weather for a city.",
            inputSchema: {
              type: "object",
              properties: { city: { type: "string" } },
            },
          },
          {
            name: "get_forecast",
            description: "Return a multi-day forecast for a city.",
            inputSchema: {
              type: "object",
              properties: { city: { type: "string" }, days: { type: "integer" } },
            },
          },
        ],
        resources: ["weather://stations"],
        prompts: ["summarize_forecast", "explain_alert"],
      });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("mcp_inventory"));
  });

  it("skill", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordSkill("refund-policy-skill", "2.1.0", {
        source: "registry://skills/refund-policy",
        manifestHash: "e".repeat(64),
        signed: true,
      });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("skill"));
  });

  it("delegation", () => {
    const f = fabric();
    decision(f, (d) => d.delegate("research-agent", "a2a", () => undefined));
    expect(normalizeSpans(captured())).toEqual(loadGolden("delegation"));
  });

  it("hook", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordHook("pii-redactor", "pre_model", {
        modified: true,
        inputHash: "a".repeat(64),
        outputHash: "b".repeat(64),
      });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("hook"));
  });

  it("file_access", () => {
    const f = fabric();
    decision(f, (d) => {
      d.recordFileAccess("/var/data/report.csv", "read", {
        contentHash: "c".repeat(64),
        sizeBytes: 2048,
        redactPath: false,
      });
      d.recordFileAccess("/patients/jane/record.pdf", "write", {
        contentHash: "d".repeat(64),
        sizeBytes: 4096,
        redactPath: true,
      });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("file_access"));
  });

  it("interaction", () => {
    resetCoverageRegistry();
    const f = fabric();
    decision(f, (d) => {
      d.recordInteraction("http.request", "https://api.example.com/v1/orders", {
        direction: "outbound",
        payloadHash: "a".repeat(64),
        metadata: { method: "POST", status: 200 },
        redactTarget: false,
      });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("interaction"));
  });

  it("interaction_tagged", () => {
    resetCoverageRegistry();
    const f = fabric();
    decision(f, (d) => {
      d.recordInteraction("db.query", "orders", {
        direction: "internal",
        payloadHash: "b".repeat(64),
        redactTarget: false,
        tags: ["atlas:AML.T0051", "owasp-llm:LLM01", "myco:risk-high"],
        baseline: { name: "db.query", status: "match" },
        signature: {
          verified: true,
          scheme: "hmac-sha256",
          keyId: "conformance-key-1",
        },
      });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("interaction_tagged"));
  });

  it("interaction_deviation", () => {
    resetCoverageRegistry();
    const f = fabric();
    decision(f, (d) => {
      d.recordInteraction("shell.exec", "/usr/bin/curl --data @/etc/passwd", {
        redactTarget: true,
        payloadHash: "c".repeat(64),
        baseline: { name: "shell.exec", status: "deviation" },
      });
    });
    expect(normalizeSpans(captured())).toEqual(loadGolden("interaction_deviation"));
  });
});
