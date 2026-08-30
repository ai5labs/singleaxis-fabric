// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import * as recorder from "../src/index.js";
// @ts-expect-error Recorder v1 does not export assurance types from the package root.
import type { EvalOptions } from "../src/index.js";
// @ts-expect-error Recorder v1 does not export runtime-control types from the package root.
import type { GuardrailResult } from "../src/index.js";

void (undefined as unknown as EvalOptions);
void (undefined as unknown as GuardrailResult);

describe("recorder-v1 package root", () => {
  it("exposes capture attributes but no assurance/control attribute families", () => {
    expect(recorder.attributes.ATTR_DECISION_ID).toBe("fabric.decision_id");
    expect(recorder.attributes.FABRIC_TOOL_ARGS_HASH).toBe("fabric.tool.arguments_hash");

    const names = Object.keys(recorder.attributes);
    expect(names.some((name) => name.includes("GUARDRAIL"))).toBe(false);
    expect(names.some((name) => name.includes("JUDGE"))).toBe(false);
    expect(names.some((name) => name.startsWith("ATTR_EVAL") || name === "EVENT_NAME_EVAL")).toBe(
      false,
    );
    expect(names.some((name) => name.includes("POLICY"))).toBe(false);
    expect(names.some((name) => name.includes("TOOL_AUTH"))).toBe(false);
    expect(names.some((name) => name.includes("ESCALAT"))).toBe(false);
  });

  it("does not hide control or evaluation methods on Decision", () => {
    const methods = Object.getOwnPropertyNames(recorder.Decision.prototype);
    for (const forbidden of [
      "recordGuardrail",
      "recordBlock",
      "requestEscalation",
      "recordEval",
      "queueJudge",
      "recordPolicyEvaluation",
      "recordToolAuthorization",
    ]) {
      expect(methods).not.toContain(forbidden);
    }
  });
});
