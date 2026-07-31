// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

/**
 * Child-span helpers for LLM and tool calls.
 *
 * A {@link Decision} wraps one agent turn; inside it the caller wraps each
 * LLM API call in `d.llmCall(...)` and each tool invocation in
 * `d.toolCall(...)`. Both produce a child span under `fabric.decision`
 * carrying the OpenTelemetry GenAI semantic conventions (`gen_ai.*`) plus
 * Fabric's `fabric.*` mirrors.
 *
 * Both namespaces are emitted: `gen_ai.*` is what observability backends
 * (Phoenix, Langfuse) key off, while the `fabric.*` mirror is kept for
 * dashboards keyed off the Fabric attributes. The setters write to both.
 */

import { SpanKind, type Span, type Tracer } from "@opentelemetry/api";

import {
  FABRIC_LLM_REQUEST_MAX_TOKENS,
  FABRIC_LLM_REQUEST_MODEL,
  FABRIC_LLM_REQUEST_TEMPERATURE,
  FABRIC_LLM_REQUEST_TOP_P,
  FABRIC_LLM_RESPONSE_FINISH_REASONS,
  FABRIC_LLM_RESPONSE_MODEL,
  FABRIC_LLM_SYSTEM,
  FABRIC_LLM_USAGE_INPUT_TOKENS,
  FABRIC_LLM_USAGE_OUTPUT_TOKENS,
  FABRIC_TOOL_ARGS_HASH,
  FABRIC_TOOL_CALL_ID,
  FABRIC_TOOL_ERROR,
  FABRIC_TOOL_ERROR_CATEGORY,
  FABRIC_TOOL_KIND,
  FABRIC_TOOL_NAME,
  FABRIC_TOOL_RESULT_COUNT,
  FABRIC_TOOL_RESULT_HASH,
  FABRIC_STEP_TYPE,
  GEN_AI_REQUEST_MAX_TOKENS,
  GEN_AI_REQUEST_MODEL,
  GEN_AI_REQUEST_PREVIOUS_RESPONSE_ID,
  GEN_AI_REQUEST_REASONING_LEVEL,
  GEN_AI_REQUEST_STREAM,
  GEN_AI_REQUEST_TEMPERATURE,
  GEN_AI_REQUEST_TOP_K,
  GEN_AI_REQUEST_TOP_P,
  GEN_AI_OPERATION_NAME,
  GEN_AI_OUTPUT_TYPE,
  GEN_AI_PROVIDER_NAME,
  GEN_AI_RESPONSE_ID,
  GEN_AI_RESPONSE_FINISH_REASONS,
  GEN_AI_RESPONSE_MODEL,
  GEN_AI_SYSTEM,
  GEN_AI_CONVERSATION_COMPACTED,
  GEN_AI_CONVERSATION_ID,
  GEN_AI_INPUT_MESSAGES,
  GEN_AI_OUTPUT_MESSAGES,
  GEN_AI_PROMPT_NAME,
  GEN_AI_PROMPT_VERSION,
  GEN_AI_SYSTEM_INSTRUCTIONS,
  GEN_AI_TOOL_DEFINITIONS,
  GEN_AI_TOOL_DESCRIPTION,
  GEN_AI_TOOL_TYPE,
  GEN_AI_TOOL_CALL_ARGUMENTS,
  GEN_AI_TOOL_CALL_RESULT,
  GEN_AI_TOOL_CALL_ID,
  GEN_AI_TOOL_NAME,
  GEN_AI_USAGE_INPUT_TOKENS,
  GEN_AI_USAGE_OUTPUT_TOKENS,
} from "./attributes.js";
import { sha256Hex } from "./hash.js";

/** Options for {@link Decision.llmCall}. */
export interface LlmCallOptions {
  /** Current GenAI provider name. */
  provider?: string;
  /** Deprecated alias for provider, retained for v0.6 compatibility. */
  system?: string;
  /** Request model id. Required. */
  model: string;
  temperature?: number;
  topP?: number;
  maxTokens?: number;
  topK?: number;
  operationName?: string;
  stream?: boolean;
  reasoningLevel?: string;
  previousResponseId?: string;
  outputType?: string;
  conversationId?: string;
  conversationCompacted?: boolean;
  promptName?: string;
  promptVersion?: string;
  systemInstructions?: unknown;
  inputMessages?: unknown;
  toolDefinitions?: unknown;
  captureContent?: boolean;
}

/** Usage metadata attached after an LLM response returns. */
export interface LlmUsage {
  inputTokens?: number;
  outputTokens?: number;
  finishReason?: string | string[];
  reasoningTokens?: number;
}

/**
 * A child span of `fabric.decision` recording one LLM API call
 * (kind=CLIENT). Obtained inside the `d.llmCall(...)` callback.
 */
export class LlmCall {
  constructor(private readonly span: Span) {}

  /**
   * Attach token counts and finish reason from the LLM response. Writes
   * both the `gen_ai.usage.*` standard attributes and the
   * `fabric.llm.usage.*` mirrors. `finishReason` always lands as a list,
   * matching the GenAI convention.
   */
  setUsage(usage: LlmUsage): void {
    if (usage.inputTokens !== undefined) {
      assertNonNegativeInt(usage.inputTokens, "inputTokens");
      this.span.setAttribute(GEN_AI_USAGE_INPUT_TOKENS, usage.inputTokens);
      this.span.setAttribute(FABRIC_LLM_USAGE_INPUT_TOKENS, usage.inputTokens);
    }
    if (usage.outputTokens !== undefined) {
      assertNonNegativeInt(usage.outputTokens, "outputTokens");
      this.span.setAttribute(GEN_AI_USAGE_OUTPUT_TOKENS, usage.outputTokens);
      this.span.setAttribute(FABRIC_LLM_USAGE_OUTPUT_TOKENS, usage.outputTokens);
    }
    if (usage.finishReason !== undefined) {
      const reasons =
        typeof usage.finishReason === "string" ? [usage.finishReason] : [...usage.finishReason];
      this.span.setAttribute(GEN_AI_RESPONSE_FINISH_REASONS, reasons);
      this.span.setAttribute(FABRIC_LLM_RESPONSE_FINISH_REASONS, reasons);
    }
    if (usage.reasoningTokens !== undefined) {
      assertNonNegativeInt(usage.reasoningTokens, "reasoningTokens");
      this.span.setAttribute(
        "gen_ai.usage.reasoning.output_tokens",
        usage.reasoningTokens,
      );
    }
  }

  /** Record the response model id (may differ from the request model). */
  setResponseModel(model: string): void {
    if (!model) {
      throw new Error("response model id must be non-empty");
    }
    this.span.setAttribute(GEN_AI_RESPONSE_MODEL, model);
    this.span.setAttribute(FABRIC_LLM_RESPONSE_MODEL, model);
  }

  setResponse(options: {
    responseId?: string;
    model?: string;
    finishReasons?: string | string[];
    outputMessages?: unknown;
  }): void {
    if (options.responseId !== undefined) this.span.setAttribute(GEN_AI_RESPONSE_ID, options.responseId);
    if (options.model !== undefined) this.setResponseModel(options.model);
    if (options.finishReasons !== undefined) {
      const reasons = typeof options.finishReasons === "string"
        ? [options.finishReasons]
        : [...options.finishReasons];
      this.span.setAttribute(GEN_AI_RESPONSE_FINISH_REASONS, reasons);
    }
    if (options.outputMessages !== undefined) {
      this.span.setAttribute(GEN_AI_OUTPUT_MESSAGES, JSON.stringify(options.outputMessages));
    }
  }

  /** Set a custom scalar attribute on the LLM call span. */
  setAttribute(key: string, value: string | number | boolean): void {
    this.span.setAttribute(key, value);
  }
}

/** Options for {@link Decision.toolCall}. */
export interface ToolCallOptions {
  /** Provider-supplied call id, e.g. `"call-1"`. */
  callId?: string;
  type?: string;
  description?: string;
  agentName?: string;
  captureContent?: boolean;
}

/**
 * A child span of `fabric.decision` recording one tool/function call
 * (kind=INTERNAL). Obtained inside the `d.toolCall(...)` callback.
 */
export class ToolCall {
  constructor(
    private readonly span: Span,
    private readonly captureContent = false,
  ) {}

  /** Record how many results/items the tool returned. */
  setResultCount(count: number): void {
    assertNonNegativeInt(count, "count");
    this.span.setAttribute(FABRIC_TOOL_RESULT_COUNT, count);
  }

  /**
   * Record a SHA-256 hash of the tool call's arguments. The caller
   * serializes their arguments to a string; only the hash
   * (`fabric.tool.arguments_hash`) lands on the span — raw args never
   * touch the trace stream.
   */
  setArguments(payload: string): void {
    this.span.setAttribute(FABRIC_TOOL_ARGS_HASH, sha256Hex(payload));
    if (this.captureContent) this.span.setAttribute(GEN_AI_TOOL_CALL_ARGUMENTS, payload);
  }

  /** Record a SHA-256 hash of the tool call's result. */
  setResult(payload: string): void {
    this.span.setAttribute(FABRIC_TOOL_RESULT_HASH, sha256Hex(payload));
    if (this.captureContent) this.span.setAttribute(GEN_AI_TOOL_CALL_RESULT, payload);
  }

  /** Record the tool's kind, e.g. `"function"`, `"retrieval"`, `"mcp"`. */
  setKind(kind: string): void {
    if (!kind) {
      throw new Error("kind must be non-empty");
    }
    this.span.setAttribute(FABRIC_TOOL_KIND, kind);
    this.span.setAttribute(GEN_AI_TOOL_TYPE, kind);
  }

  /**
   * Mark the tool call as errored without an exception being thrown (for
   * tools that *return* an error result). Stamps `fabric.tool.error=true`
   * and `fabric.tool.error_category`.
   */
  recordError(category: string): void {
    if (!category) {
      throw new Error("error category must be non-empty");
    }
    this.span.setAttribute(FABRIC_TOOL_ERROR, true);
    this.span.setAttribute(FABRIC_TOOL_ERROR_CATEGORY, category);
  }

  /** Set a custom scalar attribute on the tool call span. */
  setAttribute(key: string, value: string | number | boolean): void {
    this.span.setAttribute(key, value);
  }
}

/**
 * Start a dynamically named GenAI LLM child span and seed its request attributes.
 * Internal — the public entry point is {@link Decision.llmCall}.
 */
export function startLlmSpan(tracer: Tracer, options: LlmCallOptions): Span {
  const provider = options.provider ?? options.system;
  if (!provider) {
    throw new Error("llmCall: provider is required (e.g. 'anthropic')");
  }
  if (!options.model) {
    throw new Error("llmCall: model is required");
  }
  const operationName = options.operationName ?? "chat";
  const span = tracer.startSpan(`${operationName} ${options.model}`, { kind: SpanKind.CLIENT });
  span.setAttribute(GEN_AI_OPERATION_NAME, operationName);
  span.setAttribute(GEN_AI_PROVIDER_NAME, provider);
  span.setAttribute(FABRIC_STEP_TYPE, "llm_call");
  span.setAttribute(GEN_AI_SYSTEM, provider);
  span.setAttribute(GEN_AI_REQUEST_MODEL, options.model);
  span.setAttribute(FABRIC_LLM_SYSTEM, provider);
  span.setAttribute(FABRIC_LLM_REQUEST_MODEL, options.model);
  if (options.temperature !== undefined) {
    span.setAttribute(GEN_AI_REQUEST_TEMPERATURE, options.temperature);
    span.setAttribute(FABRIC_LLM_REQUEST_TEMPERATURE, options.temperature);
  }
  if (options.topP !== undefined) {
    span.setAttribute(GEN_AI_REQUEST_TOP_P, options.topP);
    span.setAttribute(FABRIC_LLM_REQUEST_TOP_P, options.topP);
  }
  if (options.topK !== undefined) span.setAttribute(GEN_AI_REQUEST_TOP_K, options.topK);
  if (options.maxTokens !== undefined) {
    span.setAttribute(GEN_AI_REQUEST_MAX_TOKENS, options.maxTokens);
    span.setAttribute(FABRIC_LLM_REQUEST_MAX_TOKENS, options.maxTokens);
  }
  if (options.stream) span.setAttribute(GEN_AI_REQUEST_STREAM, true);
  if (options.reasoningLevel !== undefined) span.setAttribute(GEN_AI_REQUEST_REASONING_LEVEL, options.reasoningLevel);
  if (options.previousResponseId !== undefined) span.setAttribute(GEN_AI_REQUEST_PREVIOUS_RESPONSE_ID, options.previousResponseId);
  if (options.outputType !== undefined) span.setAttribute(GEN_AI_OUTPUT_TYPE, options.outputType);
  if (options.conversationId !== undefined) span.setAttribute(GEN_AI_CONVERSATION_ID, options.conversationId);
  if (options.conversationCompacted) span.setAttribute(GEN_AI_CONVERSATION_COMPACTED, true);
  if (options.promptName !== undefined) span.setAttribute(GEN_AI_PROMPT_NAME, options.promptName);
  if (options.promptVersion !== undefined) span.setAttribute(GEN_AI_PROMPT_VERSION, options.promptVersion);
  if (options.captureContent) {
    if (options.systemInstructions !== undefined) span.setAttribute(GEN_AI_SYSTEM_INSTRUCTIONS, JSON.stringify(options.systemInstructions));
    if (options.inputMessages !== undefined) span.setAttribute(GEN_AI_INPUT_MESSAGES, JSON.stringify(options.inputMessages));
    if (options.toolDefinitions !== undefined) span.setAttribute(GEN_AI_TOOL_DEFINITIONS, JSON.stringify(options.toolDefinitions));
  }
  return span;
}

/**
 * Start a tool-named `execute_tool` child span and seed its name/call-id.
 * Internal — the public entry point is {@link Decision.toolCall}.
 */
export function startToolSpan(tracer: Tracer, name: string, options: ToolCallOptions): Span {
  if (!name) {
    throw new Error("toolCall: name is required");
  }
  const span = tracer.startSpan(name, { kind: SpanKind.INTERNAL });
  span.setAttribute(GEN_AI_OPERATION_NAME, "execute_tool");
  span.setAttribute(GEN_AI_TOOL_NAME, name);
  span.setAttribute(FABRIC_TOOL_NAME, name);
  span.setAttribute(FABRIC_STEP_TYPE, "tool_call");
  if (options.callId !== undefined) {
    span.setAttribute(GEN_AI_TOOL_CALL_ID, options.callId);
    span.setAttribute(FABRIC_TOOL_CALL_ID, options.callId);
  }
  if (options.type !== undefined) {
    span.setAttribute(GEN_AI_TOOL_TYPE, options.type);
    span.setAttribute(FABRIC_TOOL_KIND, options.type);
  }
  if (options.description !== undefined) span.setAttribute(GEN_AI_TOOL_DESCRIPTION, options.description);
  if (options.agentName !== undefined) span.setAttribute("gen_ai.agent.name", options.agentName);
  return span;
}

function assertNonNegativeInt(value: number, name: string): void {
  if (!Number.isInteger(value)) {
    throw new TypeError(`${name} must be an integer`);
  }
  if (value < 0) {
    throw new RangeError(`${name} must be non-negative`);
  }
}
