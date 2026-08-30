// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

/**
 * The `decision` primitive.
 *
 * Every agent decision is wrapped in a {@link Decision}. On open we start
 * an OTel span with Fabric's standard attributes; on close we end it.
 *
 * TypeScript has no `with` statement, so the ergonomic primary form is a
 * callback: `fabric.decision(ids, (d) => { ... })`. The decision span is
 * made the active span for the duration of the callback so child spans
 * (`d.llmCall`, `d.toolCall`) parent correctly. An explicit `start()` /
 * `end()` pair is also exposed for callers who can't nest a callback.
 */

import {
  SpanKind,
  SpanStatusCode,
  context as otelContext,
  trace,
  type Span,
  type Tracer,
} from "@opentelemetry/api";

import * as A from "./attributes.js";
import {
  ATTR_AGENT,
  ATTR_DECISION_ID,
  ATTR_EXECUTION,
  ATTR_EXECUTION_ATTEMPT,
  ATTR_EXECUTION_ATTEMPT_ID,
  ATTR_EXECUTION_RETRY_PREVIOUS_ATTEMPT_ID,
  ATTR_EXECUTION_RETRY_REASON,
  ATTR_PROFILE,
  ATTR_REQUEST,
  ATTR_SCHEMA_VERSION,
  ATTR_SESSION,
  ATTR_TENANT,
  ATTR_USER,
  ATTR_WORKFLOW,
  SCHEMA_VERSION,
  SPAN_NAME_DECISION,
} from "./attributes.js";
import { activeExecution } from "./execution.js";
import { assertSha256Hex, pythonJsonStringify, randomUuid, sha256Hex } from "./hash.js";
import {
  LlmCall,
  ToolCall,
  startLlmSpan,
  startToolSpan,
  type LlmCallOptions,
  type ToolCallOptions,
} from "./calls.js";

/** Identity passed to the {@link Decision} client identity. */
export interface DecisionClientIdentity {
  tenantId: string;
  agentId: string;
  agentName: string;
  agentVersion?: string;
  agentDescription?: string;
  profile: string;
  workflowId?: string;
  executionId?: string;
  executionAttemptId?: string;
  executionAttempt?: number;
  executionRetryReason?: string;
  executionRetryPreviousAttemptId?: string;
}

/** Per-turn identifiers for one {@link Decision}. */
export interface DecisionIds {
  sessionId: string;
  requestId: string;
  /**
   * Lineage anchor for the decision. Host-supplied verbatim; when absent the
   * SDK mints a uuid4. Independent of `requestId` (mirrors Python's
   * `decision_id` defaulting).
   */
  decisionId?: string;
  userId?: string;
  /**
   * Explicit execution-correlation id for this decision. Highest precedence:
   * `explicit DecisionIds value > active execution (ALS) > FabricConfig`. When
   * unset, the decision inherits the active {@link Execution}'s id (if any) and
   * otherwise the {@link FabricConfig} value.
   */
  executionId?: string;
  /**
   * Explicit owning workflow id for this decision. Same precedence as
   * {@link DecisionIds.executionId}.
   */
  workflowId?: string;
  workflowName?: string;
  conversationCompacted?: boolean;
}

/**
 * Side-effect replay behavior (mirrors Python `ReplayBehavior`; schema
 * `fabric.side_effect.replay_behavior` enum).
 */
export type ReplayBehavior = "replay" | "suppress" | "mock" | "manual";

const REPLAY_BEHAVIORS: readonly ReplayBehavior[] = ["replay", "suppress", "mock", "manual"];
const INTERACTION_DIRECTIONS: readonly InteractionDirection[] = ["inbound", "outbound", "internal"];
const BASELINE_STATUSES: readonly BaselineStatus[] = ["match", "deviation", "unknown"];

/** Options for {@link Decision.recordRetrieval}. */
export interface RetrievalOptions {
  /** Retrieval source label (e.g. `rag`, `kg`, `sql`, `tool`, `memory`). */
  source: string;
  /** Raw query text; hashed locally to `query_hash`, never emitted. */
  query: string;
  /** Number of results returned. */
  resultCount: number;
  /** Optional per-result hashes. */
  resultHashes?: string[];
  /** Optional caller-supplied source document ids. */
  sourceDocumentIds?: string[];
  /** Optional retrieval latency in ms. */
  latencyMs?: number;
}

/** Options for {@link Decision.remember} (a memory write). */
export interface RememberOptions {
  kind: string;
  /** Raw content; hashed locally to `content_hash`, never emitted. */
  content: string;
  key?: string;
  tags?: string[];
  ttlSeconds?: number;
  /** Names a prior memory key this write supersedes (lineage edge). */
  invalidates?: string;
}

/** Options for {@link Decision.recall} (a memory read). */
export interface RecallOptions {
  kind: string;
  key: string;
  /** Raw content; hashed locally to `content_hash`, never emitted. */
  content: string;
  source?: string;
}

/** Options for {@link Decision.recordSideEffect}. */
export interface SideEffectOptions {
  /**
   * Lineage anchor for this side effect. Host-supplied verbatim; when absent
   * the SDK mints a uuid4 (mirrors Python's `side_effect_id` defaulting).
   */
  sideEffectId?: string;
  type: string;
  targetSystem: string;
  operation: string;
  /** Raw request payload; hashed locally. Mutually exclusive with `requestHash`. */
  requestPayload?: string;
  /** Raw result payload; hashed locally. Mutually exclusive with `resultHash`. */
  resultPayload?: string;
  /** Precomputed request hash. Mutually exclusive with `requestPayload`. */
  requestHash?: string;
  /** Precomputed result hash. Mutually exclusive with `resultPayload`. */
  resultHash?: string;
  idempotencyKey?: string;
  approvalRequired?: boolean;
  committed?: boolean;
  rollbackSupported?: boolean;
  /** Replay behavior: `suppress` (default), `replay`, `mock`, or `manual`. */
  replayBehavior?: ReplayBehavior;
  parentToolCallId?: string;
}

/** Options for {@link Decision.checkpoint}. */
export interface CheckpointOptions {
  stateHash?: string;
  checkpointId?: string;
}

/** Replay envelope inputs. Only hashes and lineage identifiers are emitted. */
export interface ReplayMetadataOptions {
  stateHash?: string;
  toolResultHashes?: string[];
}

/** One privacy-preserving MCP tool definition used for inventory hashing. */
export interface McpToolDefinition {
  name: string;
  description?: string | null;
  inputSchema?: unknown;
}

export interface McpInventoryOptions {
  server: string;
  transport: string;
  tools: McpToolDefinition[];
  resources?: unknown[];
  prompts?: unknown[];
}

export interface SkillOptions {
  source?: string;
  manifestHash?: string;
  signed?: boolean;
}

export interface HookOptions {
  modified: boolean;
  inputHash?: string;
  outputHash?: string;
}

export interface FileAccessOptions {
  contentHash?: string;
  sizeBytes?: number;
  /** Hash the path locally instead of emitting it. Defaults to true. */
  redactPath?: boolean;
}

export type InteractionDirection = "inbound" | "outbound" | "internal";
export type BaselineStatus = "match" | "deviation" | "unknown";

export interface InteractionOptions {
  direction?: InteractionDirection;
  /** Caller-supplied hash of the interaction payload; raw payload is never accepted. */
  payloadHash?: string;
  /** Metadata is canonicalized and hashed locally; it is never emitted in clear. */
  metadata?: Record<string, unknown>;
  /** Hash the target locally instead of emitting it. Defaults to true. */
  redactTarget?: boolean;
  tags?: string[];
  baseline?: { name: string; status: BaselineStatus };
  /** Result of host-side signature verification; no key or artifact is accepted. */
  signature?: { verified: boolean; scheme: string; keyId?: string };
}

const COVERED_INTERACTION_KINDS = new Set<string>();

/** Reset one-shot generic coverage signals. Primarily useful for isolated test processes. */
export function resetCoverageRegistry(): void {
  COVERED_INTERACTION_KINDS.clear();
}

/**
 * One agent turn. Not safe to share across async tasks — open one
 * `Decision` per turn.
 */
export class Decision {
  private readonly tracer: Tracer;
  private readonly span: Span;
  // Rolling counters + distinct-value sets folded onto the decision span,
  // mirroring the Python SDK so the Telemetry Bridge can summarize a
  // decision without replaying every event.
  private retrievalCount = 0;
  private readonly retrievalSources = new Set<string>();
  private memoryWriteCount = 0;
  private memoryReadCount = 0;
  private memoryEraseCount = 0;
  private readonly memoryKinds = new Set<string>();
  private sideEffectCount = 0;
  private readonly sideEffectTypes = new Set<string>();
  private readonly sideEffectSystems = new Set<string>();
  private checkpointCount = 0;
  private readonly decisionId: string;
  private readonly resolvedExecutionId?: string;
  private readonly checkpointIds: string[] = [];
  private readonly suppressedSideEffectIds: string[] = [];
  private skillCount = 0;
  private delegationCount = 0;
  private delegationDepth = 0;
  private hookCount = 0;
  private fileAccessCount = 0;
  private interactionCount = 0;
  private readonly interactionKinds = new Set<string>();

  constructor(tracer: Tracer, span: Span, identity: DecisionClientIdentity, ids: DecisionIds) {
    this.tracer = tracer;
    this.span = span;
    span.setAttribute(ATTR_SCHEMA_VERSION, SCHEMA_VERSION);
    span.setAttribute(ATTR_TENANT, identity.tenantId);
    span.setAttribute(ATTR_AGENT, identity.agentId);
    span.setAttribute(ATTR_PROFILE, identity.profile);
    span.setAttribute("gen_ai.operation.name", "invoke_agent");
    span.setAttribute("gen_ai.agent.name", identity.agentName);
    span.setAttribute("gen_ai.agent.id", identity.agentId);
    span.setAttribute("gen_ai.conversation.id", ids.sessionId);
    if (identity.agentVersion !== undefined) {
      span.setAttribute("gen_ai.agent.version", identity.agentVersion);
    }
    if (identity.agentDescription !== undefined) {
      span.setAttribute("gen_ai.agent.description", identity.agentDescription);
    }
    if (ids.conversationCompacted) {
      span.setAttribute("gen_ai.conversation.compacted", true);
    }
    // Resolve the execution-correlation metadata with precedence:
    //   explicit DecisionIds value > active execution (ALS) > FabricConfig.
    // A decision opened outside any execution falls back to the config exactly
    // as before (`active` is undefined), so its emitted bytes are unchanged.
    const active = activeExecution();
    const workflowId = ids.workflowId ?? active?.workflowId ?? identity.workflowId;
    const executionId = ids.executionId ?? active?.executionId ?? identity.executionId;
    this.resolvedExecutionId = executionId;
    // The attempt/retry metadata has no per-decision kwarg, so it is inherited
    // from the active execution when present and otherwise the config value.
    const executionAttemptId = active?.executionAttemptId ?? identity.executionAttemptId;
    const executionAttempt = active?.executionAttempt ?? identity.executionAttempt;
    const executionRetryReason = active?.executionRetryReason ?? identity.executionRetryReason;
    const executionRetryPreviousAttemptId =
      active?.executionRetryPreviousAttemptId ?? identity.executionRetryPreviousAttemptId;
    if (workflowId !== undefined) {
      span.setAttribute(ATTR_WORKFLOW, workflowId);
      span.setAttribute("gen_ai.workflow.name", ids.workflowName ?? workflowId);
    }
    if (executionId !== undefined) {
      span.setAttribute(ATTR_EXECUTION, executionId);
    }
    if (executionAttemptId !== undefined) {
      span.setAttribute(ATTR_EXECUTION_ATTEMPT_ID, executionAttemptId);
    }
    if (executionAttempt !== undefined) {
      span.setAttribute(ATTR_EXECUTION_ATTEMPT, executionAttempt);
    }
    if (executionRetryReason !== undefined) {
      span.setAttribute(ATTR_EXECUTION_RETRY_REASON, executionRetryReason);
    }
    if (executionRetryPreviousAttemptId !== undefined) {
      span.setAttribute(ATTR_EXECUTION_RETRY_PREVIOUS_ATTEMPT_ID, executionRetryPreviousAttemptId);
    }
    span.setAttribute(ATTR_SESSION, ids.sessionId);
    span.setAttribute(ATTR_REQUEST, ids.requestId);
    this.decisionId = ids.decisionId ?? randomUuid();
    span.setAttribute(ATTR_DECISION_ID, this.decisionId);
    if (ids.userId !== undefined) {
      span.setAttribute(ATTR_USER, ids.userId);
    }
  }

  /**
   * Wrap one LLM API call in a `{operation} {model}` child span (kind=CLIENT).
   * The span is active for the duration of `fn` and ended afterwards. A
   * thrown error is recorded on the span and re-thrown.
   */
  llmCall<T>(options: LlmCallOptions, fn: (call: LlmCall) => T): T {
    const span = startLlmSpan(this.tracer, options);
    const ctx = trace.setSpan(otelContext.active(), span);
    return otelContext.with(ctx, () => {
      const call = new LlmCall(span, options.captureContent ?? false);
      return runAndEnd(span, () => fn(call));
    });
  }

  /**
   * Wrap one tool/function call in a tool-named `execute_tool` child span
   * (kind=INTERNAL). The span is active for the duration of `fn`.
   */
  toolCall<T>(name: string, options: ToolCallOptions, fn: (tool: ToolCall) => T): T {
    const span = startToolSpan(this.tracer, name, options);
    const ctx = trace.setSpan(otelContext.active(), span);
    return otelContext.with(ctx, () => {
      const tool = new ToolCall(span, options.captureContent ?? false);
      return runAndEnd(span, () => fn(tool));
    });
  }

  /**
   * Record a retrieval (RAG/KG/SQL/tool/memory) as a `fabric.retrieval`
   * event. The raw query is hashed locally; rolling `fabric.retrieval_count`
   * and `fabric.retrieval_sources` are folded onto the decision span.
   */
  recordRetrieval(options: RetrievalOptions): void {
    this.retrievalCount += 1;
    this.retrievalSources.add(options.source);
    this.span.setAttribute(A.ATTR_RETRIEVAL_COUNT, this.retrievalCount);
    this.span.setAttribute(A.ATTR_RETRIEVAL_SOURCES, sortedSet(this.retrievalSources));

    const attrs: Record<string, string | number | boolean | string[]> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_RETRIEVAL_SOURCE]: options.source,
      [A.ATTR_RETRIEVAL_QUERY_HASH]: sha256Hex(options.query),
      [A.ATTR_RETRIEVAL_RESULT_COUNT]: options.resultCount,
    };
    if (options.resultHashes && options.resultHashes.length > 0) {
      attrs[A.ATTR_RETRIEVAL_RESULT_HASHES] = options.resultHashes.map((value) =>
        assertSha256Hex("recordRetrieval: resultHashes", value),
      );
    }
    if (options.sourceDocumentIds && options.sourceDocumentIds.length > 0) {
      attrs[A.ATTR_RETRIEVAL_SOURCE_DOC_IDS] = [...options.sourceDocumentIds];
    }
    if (options.latencyMs !== undefined) {
      attrs[A.ATTR_RETRIEVAL_LATENCY_MS] = options.latencyMs;
    }
    this.span.addEvent(A.EVENT_NAME_RETRIEVAL, attrs);
  }

  /**
   * Record a memory WRITE as a `fabric.memory` event (direction=`write`).
   * Raw content is hashed locally to `content_hash`.
   */
  remember(options: RememberOptions): void {
    this.memoryWriteCount += 1;
    this.memoryKinds.add(options.kind);
    this.updateMemoryCounters();
    const attrs: Record<string, string | number | boolean | string[]> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_MEMORY_DIRECTION]: "write",
      [A.ATTR_MEMORY_KIND]: options.kind,
      [A.ATTR_MEMORY_CONTENT_HASH]: sha256Hex(options.content),
    };
    if (options.key !== undefined) {
      attrs[A.ATTR_MEMORY_KEY] = options.key;
    }
    if (options.tags && options.tags.length > 0) {
      attrs[A.ATTR_MEMORY_TAGS] = [...options.tags];
    }
    if (options.ttlSeconds !== undefined) {
      attrs[A.ATTR_MEMORY_TTL_SECONDS] = options.ttlSeconds;
    }
    if (options.invalidates !== undefined) {
      attrs[A.ATTR_MEMORY_INVALIDATES] = options.invalidates;
    }
    this.span.addEvent(A.EVENT_NAME_MEMORY, attrs);
  }

  /**
   * Record a memory READ as a `fabric.memory` event (direction=`read`).
   * Uses the same `content_hash` strategy as {@link remember} so reads and
   * writes can be correlated by hash downstream.
   */
  recall(options: RecallOptions): void {
    this.memoryReadCount += 1;
    this.memoryKinds.add(options.kind);
    this.updateMemoryCounters();
    const attrs: Record<string, string | number | boolean | string[]> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_MEMORY_DIRECTION]: "read",
      [A.ATTR_MEMORY_KIND]: options.kind,
      [A.ATTR_MEMORY_KEY]: options.key,
      [A.ATTR_MEMORY_CONTENT_HASH]: sha256Hex(options.content),
    };
    if (options.source !== undefined) {
      attrs[A.ATTR_MEMORY_SOURCE] = options.source;
    }
    this.span.addEvent(A.EVENT_NAME_MEMORY, attrs);
  }

  /**
   * Emit a right-to-erasure marker as a `fabric.memory` event
   * (direction=`erase`). The OSS SDK only emits the marker — it deletes
   * nothing. An erase references a key, not content, so no hash is produced.
   */
  forget(kind: string, key: string, options: { tenantScope?: boolean } = {}): void {
    this.memoryEraseCount += 1;
    this.memoryKinds.add(kind);
    this.updateMemoryCounters();
    this.span.setAttribute(A.ATTR_MEMORY_ERASE_COUNT, this.memoryEraseCount);
    const attrs: Record<string, string | number | boolean> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_MEMORY_DIRECTION]: "erase",
      [A.ATTR_MEMORY_KIND]: kind,
      [A.ATTR_MEMORY_KEY]: key,
    };
    if (options.tenantScope === true) {
      attrs[A.ATTR_MEMORY_TENANT_SCOPE] = true;
    }
    this.span.addEvent(A.EVENT_NAME_MEMORY, attrs);
  }

  private updateMemoryCounters(): void {
    this.span.setAttribute(A.ATTR_MEMORY_WRITE_COUNT, this.memoryWriteCount);
    this.span.setAttribute(A.ATTR_MEMORY_READ_COUNT, this.memoryReadCount);
    this.span.setAttribute(A.ATTR_MEMORY_KINDS, sortedSet(this.memoryKinds));
  }

  /**
   * Record an external mutation (CRM write, ticket, email, payment, …) as a
   * `fabric.side_effect` event. Raw payloads are hashed locally; pass either
   * the raw payload OR a precomputed hash per field, not both.
   */
  recordSideEffect(options: SideEffectOptions): void {
    if (options.replayBehavior !== undefined) {
      assertOneOf("recordSideEffect: replayBehavior", options.replayBehavior, REPLAY_BEHAVIORS);
    }
    if (options.requestPayload !== undefined && options.requestHash !== undefined) {
      throw new Error("pass either requestPayload or requestHash, not both");
    }
    if (options.resultPayload !== undefined && options.resultHash !== undefined) {
      throw new Error("pass either resultPayload or resultHash, not both");
    }
    const requestHash =
      options.requestPayload !== undefined
        ? sha256Hex(options.requestPayload)
        : options.requestHash === undefined
          ? undefined
          : assertSha256Hex("recordSideEffect: requestHash", options.requestHash);
    const resultHash =
      options.resultPayload !== undefined
        ? sha256Hex(options.resultPayload)
        : options.resultHash === undefined
          ? undefined
          : assertSha256Hex("recordSideEffect: resultHash", options.resultHash);

    this.sideEffectCount += 1;
    this.sideEffectTypes.add(options.type);
    this.sideEffectSystems.add(options.targetSystem);
    this.span.setAttribute(A.ATTR_SIDE_EFFECT_COUNT, this.sideEffectCount);
    this.span.setAttribute(A.ATTR_SIDE_EFFECT_TYPES, sortedSet(this.sideEffectTypes));
    this.span.setAttribute(A.ATTR_SIDE_EFFECT_SYSTEMS, sortedSet(this.sideEffectSystems));

    const sideEffectId = options.sideEffectId ?? randomUuid();
    if ((options.replayBehavior ?? "suppress") === "suppress") {
      this.suppressedSideEffectIds.push(sideEffectId);
    }
    const attrs: Record<string, string | number | boolean> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_SE_ID]: sideEffectId,
      [A.ATTR_SE_TYPE]: options.type,
      [A.ATTR_SE_TARGET_SYSTEM]: options.targetSystem,
      [A.ATTR_SE_OPERATION]: options.operation,
      [A.ATTR_SE_APPROVAL_REQUIRED]: options.approvalRequired ?? false,
      [A.ATTR_SE_COMMITTED]: options.committed ?? true,
      [A.ATTR_SE_ROLLBACK_SUPPORTED]: options.rollbackSupported ?? false,
      [A.ATTR_SE_REPLAY_BEHAVIOR]: options.replayBehavior ?? "suppress",
    };
    if (requestHash !== undefined) {
      attrs[A.ATTR_SE_REQUEST_HASH] = requestHash;
    }
    if (resultHash !== undefined) {
      attrs[A.ATTR_SE_RESULT_HASH] = resultHash;
    }
    if (options.idempotencyKey !== undefined) {
      attrs[A.ATTR_SE_IDEMPOTENCY_KEY] = options.idempotencyKey;
    }
    if (options.parentToolCallId !== undefined) {
      attrs[A.ATTR_SE_PARENT_TOOL_CALL_ID] = options.parentToolCallId;
    }
    this.span.addEvent(A.EVENT_NAME_SIDE_EFFECT, attrs);
  }

  /**
   * Mark a save point on the decision timeline as a `fabric.checkpoint`
   * event. Multiple checkpoints per decision are allowed.
   */
  checkpoint(stepName: string, options: CheckpointOptions = {}): void {
    this.checkpointCount += 1;
    this.span.setAttribute(A.ATTR_CHECKPOINT_COUNT, this.checkpointCount);
    const checkpointId = options.checkpointId ?? randomUuid();
    this.checkpointIds.push(checkpointId);
    const attrs: Record<string, string | number | boolean> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_CHECKPOINT_ID]: checkpointId,
      [A.ATTR_CHECKPOINT_STEP_NAME]: stepName,
    };
    if (options.stateHash !== undefined) {
      attrs[A.ATTR_CHECKPOINT_STATE_HASH] = assertSha256Hex(
        "checkpoint: stateHash",
        options.stateHash,
      );
    }
    this.span.addEvent(A.EVENT_NAME_CHECKPOINT, attrs);
  }

  /** Emit a replay envelope derived from checkpoints and suppressed side effects in this decision. */
  recordReplayMetadata(options: ReplayMetadataOptions = {}): void {
    const attrs: Record<string, string | string[]> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_REPLAY_METADATA_VERSION]: "1",
      [A.ATTR_REPLAY_DECISION_ID]: this.decisionId,
    };
    if (this.resolvedExecutionId !== undefined) {
      attrs[A.ATTR_REPLAY_EXECUTION_ID] = this.resolvedExecutionId;
    }
    if (this.checkpointIds.length > 0) {
      attrs[A.ATTR_REPLAY_CHECKPOINT_IDS] = [...this.checkpointIds];
    }
    if (this.suppressedSideEffectIds.length > 0) {
      attrs[A.ATTR_REPLAY_SUPPRESSED_SIDE_EFFECT_IDS] = [...this.suppressedSideEffectIds];
    }
    if (options.stateHash !== undefined) {
      attrs[A.ATTR_REPLAY_STATE_HASH] = assertSha256Hex(
        "recordReplayMetadata: stateHash",
        options.stateHash,
      );
    }
    if (options.toolResultHashes && options.toolResultHashes.length > 0) {
      attrs[A.ATTR_REPLAY_TOOL_RESULT_HASHES] = options.toolResultHashes.map((value) =>
        assertSha256Hex("recordReplayMetadata: toolResultHashes", value),
      );
    }
    this.span.addEvent(A.EVENT_NAME_REPLAY, attrs);
  }

  /** Record an MCP server's advertised surface using definition hashes, never raw schemas. */
  recordMcpInventory(options: McpInventoryOptions): {
    tools: string[];
    toolsHash: string;
  } {
    assertNonEmpty("recordMcpInventory: server", options.server);
    assertNonEmpty("recordMcpInventory: transport", options.transport);
    for (const tool of options.tools) {
      assertNonEmpty("recordMcpInventory: tool name", tool.name);
    }
    const definitions = options.tools.map((tool) => ({
      name: tool.name,
      description: tool.description ?? null,
      inputSchema: tool.inputSchema ?? null,
    }));
    const tools = definitions.map(
      (definition) =>
        `${definition.name}:${sha256Hex(pythonJsonStringify(definition)).slice(0, 12)}`,
    );
    const toolsHash = sha256Hex(pythonJsonStringify(definitions));
    const attrs: Record<string, string | number | string[]> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_MCP_SERVER]: options.server,
      [A.ATTR_MCP_TRANSPORT]: options.transport,
      [A.ATTR_MCP_TOOL_COUNT]: definitions.length,
      [A.ATTR_MCP_TOOLS]: tools,
      [A.ATTR_MCP_TOOLS_HASH]: toolsHash,
    };
    if (options.resources !== undefined) {
      attrs[A.ATTR_MCP_RESOURCE_COUNT] = options.resources.length;
    }
    if (options.prompts !== undefined) {
      attrs[A.ATTR_MCP_PROMPT_COUNT] = options.prompts.length;
    }
    this.span.addEvent(A.EVENT_NAME_MCP_INVENTORY, attrs);
    return { tools, toolsHash };
  }

  /** Record a loaded skill by identity and integrity metadata. */
  recordSkill(name: string, version: string, options: SkillOptions = {}): void {
    assertNonEmpty("recordSkill: name", name);
    assertNonEmpty("recordSkill: version", version);
    this.skillCount += 1;
    this.span.setAttribute(A.ATTR_SKILL_COUNT, this.skillCount);
    const attrs: Record<string, string | boolean> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_SKILL_NAME]: name,
      [A.ATTR_SKILL_VERSION]: version,
    };
    if (options.source !== undefined) attrs[A.ATTR_SKILL_SOURCE] = options.source;
    if (options.manifestHash !== undefined) {
      attrs[A.ATTR_SKILL_MANIFEST_HASH] = assertSha256Hex(
        "recordSkill: manifestHash",
        options.manifestHash,
      );
    }
    if (options.signed !== undefined) attrs[A.ATTR_SKILL_SIGNED] = options.signed;
    this.span.addEvent(A.EVENT_NAME_SKILL, attrs);
  }

  /** Run a delegated operation while recording only the target agent, protocol, and depth. */
  delegate<T>(toAgent: string, protocol: string, fn: () => T): T {
    assertNonEmpty("delegate: toAgent", toAgent);
    assertNonEmpty("delegate: protocol", protocol);
    this.delegationCount += 1;
    this.delegationDepth += 1;
    this.span.setAttribute(A.ATTR_DELEGATION_COUNT, this.delegationCount);
    this.span.addEvent(A.EVENT_NAME_DELEGATION, {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_DELEGATION_TO_AGENT]: toAgent,
      [A.ATTR_DELEGATION_PROTOCOL]: protocol,
      [A.ATTR_DELEGATION_DEPTH]: this.delegationDepth,
    });
    let result: T;
    try {
      result = fn();
    } catch (error) {
      this.delegationDepth -= 1;
      throw error;
    }
    if (isThenable(result)) {
      return result.then(
        (value) => {
          this.delegationDepth -= 1;
          return value;
        },
        (error: unknown) => {
          this.delegationDepth -= 1;
          throw error;
        },
      ) as T;
    }
    this.delegationDepth -= 1;
    return result;
  }

  /** Record a lifecycle hook without accepting raw hook inputs or outputs. */
  recordHook(name: string, phase: string, options: HookOptions): void {
    assertNonEmpty("recordHook: name", name);
    assertNonEmpty("recordHook: phase", phase);
    this.hookCount += 1;
    this.span.setAttribute(A.ATTR_HOOK_COUNT, this.hookCount);
    const attrs: Record<string, string | boolean> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_HOOK_NAME]: name,
      [A.ATTR_HOOK_PHASE]: phase,
      [A.ATTR_HOOK_MODIFIED]: options.modified,
    };
    if (options.inputHash !== undefined) {
      attrs[A.ATTR_HOOK_INPUT_HASH] = assertSha256Hex("recordHook: inputHash", options.inputHash);
    }
    if (options.outputHash !== undefined) {
      attrs[A.ATTR_HOOK_OUTPUT_HASH] = assertSha256Hex(
        "recordHook: outputHash",
        options.outputHash,
      );
    }
    this.span.addEvent(A.EVENT_NAME_HOOK, attrs);
  }

  /** Record filesystem access, with opt-in local path hashing for sensitive paths. */
  recordFileAccess(path: string, operation: string, options: FileAccessOptions = {}): void {
    assertNonEmpty("recordFileAccess: path", path);
    assertNonEmpty("recordFileAccess: operation", operation);
    if (options.sizeBytes !== undefined) {
      assertNonNegativeInt("recordFileAccess: sizeBytes", options.sizeBytes);
    }
    this.fileAccessCount += 1;
    this.span.setAttribute(A.ATTR_FILE_ACCESS_COUNT, this.fileAccessCount);
    const redactPath = options.redactPath ?? true;
    const attrs: Record<string, string | number | boolean> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_FILE_OPERATION]: operation,
      [A.ATTR_FILE_PATH_REDACTED]: redactPath,
    };
    if (redactPath) attrs[A.ATTR_FILE_PATH_HASH] = sha256Hex(path);
    else attrs[A.ATTR_FILE_PATH] = path;
    if (options.contentHash !== undefined) {
      attrs[A.ATTR_FILE_CONTENT_HASH] = assertSha256Hex(
        "recordFileAccess: contentHash",
        options.contentHash,
      );
    }
    if (options.sizeBytes !== undefined) attrs[A.ATTR_FILE_SIZE_BYTES] = options.sizeBytes;
    this.span.addEvent(A.EVENT_NAME_FILE, attrs);
  }

  /**
   * Capture an open-vocabulary interaction. Payloads are hash-only; metadata
   * is canonicalized and hashed; sensitive targets can be locally hashed.
   */
  recordInteraction(kind: string, target: string, options: InteractionOptions = {}): void {
    assertNonEmpty("recordInteraction: kind", kind);
    assertNonEmpty("recordInteraction: target", target);
    if (options.direction !== undefined) {
      assertOneOf("recordInteraction: direction", options.direction, INTERACTION_DIRECTIONS);
    }
    if (options.baseline !== undefined) {
      assertNonEmpty("recordInteraction: baseline name", options.baseline.name);
      assertOneOf("recordInteraction: baseline status", options.baseline.status, BASELINE_STATUSES);
    }
    this.interactionCount += 1;
    this.interactionKinds.add(kind);
    this.span.setAttribute(A.ATTR_INTERACTION_COUNT, this.interactionCount);
    this.span.setAttribute(A.ATTR_INTERACTION_KINDS, sortedSet(this.interactionKinds));
    const redactTarget = options.redactTarget ?? true;
    const attrs: Record<string, string | boolean | string[]> = {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_INTERACTION_KIND]: kind,
      [A.ATTR_INTERACTION_TARGET_REDACTED]: redactTarget,
    };
    if (redactTarget) attrs[A.ATTR_INTERACTION_TARGET_HASH] = sha256Hex(target);
    else attrs[A.ATTR_INTERACTION_TARGET] = target;
    if (options.direction !== undefined) {
      attrs[A.ATTR_INTERACTION_DIRECTION] = options.direction;
    }
    if (options.payloadHash !== undefined) {
      attrs[A.ATTR_INTERACTION_PAYLOAD_HASH] = assertSha256Hex(
        "recordInteraction: payloadHash",
        options.payloadHash,
      );
    }
    if (options.metadata !== undefined) {
      attrs[A.ATTR_INTERACTION_METADATA_HASH] = sha256Hex(pythonJsonStringify(options.metadata));
    }
    if (options.tags && options.tags.length > 0) attrs[A.ATTR_TAGS] = [...options.tags];
    if (options.baseline !== undefined) {
      attrs[A.ATTR_BASELINE_NAME] = options.baseline.name;
      attrs[A.ATTR_BASELINE_STATUS] = options.baseline.status;
    }
    if (options.signature !== undefined) {
      attrs[A.ATTR_SIGNATURE_VERIFIED] = options.signature.verified;
      attrs[A.ATTR_SIGNATURE_SCHEME] = options.signature.scheme;
      if (options.signature.keyId !== undefined) {
        attrs[A.ATTR_SIGNATURE_KEY_ID] = options.signature.keyId;
      }
    }
    this.span.addEvent(A.EVENT_NAME_INTERACTION, attrs);

    if (!COVERED_INTERACTION_KINDS.has(kind)) {
      COVERED_INTERACTION_KINDS.add(kind);
      this.recordCoverage(kind, "new_kind");
    }
    if (options.baseline?.status === "deviation" && (!options.tags || options.tags.length === 0)) {
      this.recordCoverage(kind, "unclassified_deviation");
    }
  }

  private recordCoverage(kind: string, reason: string): void {
    this.span.addEvent(A.EVENT_NAME_COVERAGE, {
      [ATTR_SCHEMA_VERSION]: SCHEMA_VERSION,
      [A.ATTR_COVERAGE_KIND]: kind,
      [A.ATTR_COVERAGE_SUGGESTION]: "generic",
      [A.ATTR_COVERAGE_REASON]: reason,
    });
  }

  /** Set a custom scalar attribute on the decision span. */
  setAttribute(key: string, value: string | number | boolean): void {
    assertScalarAttribute(key, value);
    this.span.setAttribute(key, value);
  }

  /** The live OTel span for this decision. */
  getSpan(): Span {
    return this.span;
  }

  /** End the decision span. Used by the explicit start/end form. */
  end(): void {
    this.span.end();
  }
}

/**
 * Run `fn`, ending `span` afterwards. Async-aware: if `fn` returns a
 * thenable (a Promise), the span is NOT ended until that promise settles,
 * so setters called inside an awaited callback body land BEFORE the span
 * closes. For a synchronous `fn`, the span ends synchronously in a
 * `try/finally` exactly as before. On a thrown error (or rejection), the
 * exception + ERROR status is recorded (matching the OTel default) before
 * the error propagates.
 */
function runAndEnd<T>(span: Span, fn: () => T): T {
  let result: T;
  try {
    result = fn();
  } catch (err) {
    recordError(span, err);
    span.end();
    throw err;
  }
  if (isThenable(result)) {
    return result.then(
      (value) => {
        span.end();
        return value;
      },
      (err: unknown) => {
        recordError(span, err);
        span.end();
        throw err;
      },
    ) as T;
  }
  span.end();
  return result;
}

/** Record an exception + ERROR status on `span` (does not end it). */
function recordError(span: Span, err: unknown): void {
  span.setStatus({ code: SpanStatusCode.ERROR, message: errorName(err) });
  if (err instanceof Error) {
    span.recordException(err);
  }
}

/**
 * Robust thenable check — true for any value exposing a `.then` method
 * (native Promises and Promise-likes), used to defer span-ending until an
 * async callback settles.
 */
function isThenable(value: unknown): value is PromiseLike<unknown> {
  return (
    value != null &&
    (typeof value === "object" || typeof value === "function") &&
    typeof (value as { then?: unknown }).then === "function"
  );
}

function errorName(err: unknown): string {
  if (err instanceof Error) {
    return err.name;
  }
  return "Error";
}

/** Distinct values of a set, lexicographically sorted — matches Python's
 * `sorted({...})` used for the rolling distinct-value span attributes. */
function sortedSet(values: Set<string>): string[] {
  return [...values].sort();
}

/**
 * Runtime telemetry guards. TS types protect compile-time callers, but
 * plain-JS callers can pass anything; these helpers fail loud (throw a
 * specific Error) rather than letting an out-of-contract span be emitted,
 * matching the Python SDK's posture. They never run for valid inputs, so the
 * emitted shape — and the conformance goldens — stay byte-identical.
 */

/** Throw unless `value` is a JSON scalar (string | number | boolean). */
function assertScalarAttribute(key: string, value: unknown): void {
  const t = typeof value;
  if (t === "string" || t === "boolean") {
    return;
  }
  if (t === "number") {
    assertFiniteNumber(key, value as number);
    return;
  }
  throw new Error(
    `setAttribute: value for "${key}" must be a string, number, or boolean; got ${describe(value)}`,
  );
}

/** Throw if `value` is a non-finite number (NaN / Infinity / -Infinity). */
function assertFiniteNumber(field: string, value: number): void {
  if (!Number.isFinite(value)) {
    throw new Error(`${field} must be a finite number; got ${String(value)}`);
  }
}

function assertNonNegativeInt(field: string, value: number): void {
  if (!Number.isInteger(value) || value < 0) {
    throw new Error(`${field} must be a non-negative integer; got ${String(value)}`);
  }
}

function assertNonEmpty(field: string, value: string): void {
  if (value.trim() === "") {
    throw new Error(`${field} must be non-empty`);
  }
}

/** Throw unless `value` is one of `allowed`. */
function assertOneOf<T extends string>(field: string, value: unknown, allowed: readonly T[]): void {
  if (typeof value !== "string" || !allowed.includes(value as T)) {
    throw new Error(`${field} must be one of {${allowed.join(", ")}}; got ${describe(value)}`);
  }
}

/** Render an offending value for an error message. */
function describe(value: unknown): string {
  if (typeof value === "string") {
    return JSON.stringify(value);
  }
  if (value === null) {
    return "null";
  }
  if (typeof value === "object" || typeof value === "function") {
    return typeof value;
  }
  return String(value);
}

/**
 * Start a decision span and run `fn` with it active, then end it.
 * Internal — the public entry point is `Fabric.decision`.
 *
 * The span is installed as the active context via `context.with(...)` (the
 * same mechanism `llmCall`/`toolCall` use) rather than
 * `startActiveSpan`'s callback scope, so the decision span stays active for
 * the synchronous portion of an async body — long enough for child
 * `llmCall`/`toolCall` spans opened before the first `await` to parent
 * under it. Span-ending is async-aware via {@link runAndEnd}: a sync `fn`
 * ends synchronously, while an async `fn`'s span is ended only once the
 * returned promise settles.
 */
export function runDecision<T>(
  tracer: Tracer,
  identity: DecisionClientIdentity,
  ids: DecisionIds,
  fn: (d: Decision) => T,
): T {
  validateIds(ids);
  const span = tracer.startSpan(SPAN_NAME_DECISION, { kind: SpanKind.INTERNAL });
  const ctx = trace.setSpan(otelContext.active(), span);
  return otelContext.with(ctx, () => {
    const decision = new Decision(tracer, span, identity, ids);
    return runAndEnd(span, () => fn(decision));
  });
}

/**
 * Start a decision span WITHOUT a callback. The caller must invoke
 * `d.end()`. Note: with this form the decision span is not installed as
 * the active context, so child `llmCall`/`toolCall` spans will not parent
 * under it automatically — prefer the callback form for the trace tree.
 */
export function startDecision(
  tracer: Tracer,
  identity: DecisionClientIdentity,
  ids: DecisionIds,
): Decision {
  validateIds(ids);
  const span = tracer.startSpan(SPAN_NAME_DECISION, { kind: SpanKind.INTERNAL });
  return new Decision(tracer, span, identity, ids);
}

function validateIds(ids: DecisionIds): void {
  if (!ids.sessionId) {
    throw new Error("sessionId is required");
  }
  if (!ids.requestId) {
    throw new Error("requestId is required");
  }
}
