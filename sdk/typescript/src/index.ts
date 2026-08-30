// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

/**
 * `@singleaxis/fabric` — the TypeScript capture core for SingleAxis
 * Fabric. Emits the same `fabric.*` / `gen_ai.*` span + event wire
 * contract as the Python SDK.
 */

export { Fabric, TRACER_NAME, type FabricConfig } from "./client.js";
export {
  Decision,
  type CheckpointOptions,
  type BaselineStatus,
  type DecisionClientIdentity,
  type DecisionIds,
  type FileAccessOptions,
  type HookOptions,
  type InteractionDirection,
  type InteractionOptions,
  type McpInventoryOptions,
  type McpToolDefinition,
  type RecallOptions,
  type RememberOptions,
  type ReplayBehavior,
  type ReplayMetadataOptions,
  type RetrievalOptions,
  type SideEffectOptions,
  type SkillOptions,
  resetCoverageRegistry,
} from "./decision.js";
export { Execution, type ExecutionOptions } from "./execution.js";
export {
  LlmCall,
  ToolCall,
  type LlmCallOptions,
  type LlmCacheUsage,
  type LlmUsage,
  type ToolCallOptions,
} from "./calls.js";
export { canonicalObjectHash, sha256Hex } from "./hash.js";
export * as attributes from "./recorder-attributes.js";
