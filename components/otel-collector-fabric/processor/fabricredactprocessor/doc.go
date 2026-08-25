// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

// Package fabricredactprocessor is an OpenTelemetry Collector logs and traces
// processor that forwards content-bearing pdata fields to the Fabric Presidio
// sidecar for PII detection and deterministic hashing. It mirrors the Bridge's
// in-process Presidio stage so operators who run the Collector topology get
// equivalent fail-closed redaction semantics without adopting the full Bridge.
//
// Per-field wire contract: POST http://unix/v1/redact
//
//	request  : {"path": "<class>.<attr>", "value": "<string>"}
//	response : {"value": "...", "hashed": bool, "pii_category": "..."}
//
// Traversal inventory:
//   - logs: resource/scope attributes and schema URLs; scope name/version; log
//     body (including nested map/slice/bytes); severity text; record attributes
//   - traces: the same resource/scope surfaces; span name, trace state, status
//     message and attributes; event names/attributes; link trace state/attributes
//   - structural numeric/binary fields (IDs, timestamps, flags, counts, status
//     code, span kind, severity number) are preserved; numeric attribute values
//     are inspected and dropped if a replacement would change their type;
//     attribute/map keys are inspected but never renamed (a changed key drops
//     the payload to avoid key collisions); skip_attributes is an explicit raw
//     key/value exemption
//
// Valid UTF-8 byte values use the strings-only sidecar contract and remain byte
// values after replacement. Non-UTF-8 bytes fail closed by default. Operators
// can explicitly choose reject or legacy passthrough behavior with byte_handling.
// Schema URLs and trace state are cleared when redaction changes them so the
// processor never emits malformed protocol metadata.
//
// Fail-closed: on any transport, protocol, or HTTP-status error the
// offending record is dropped. This matches spec 004 §A's
// deny-by-default posture — better to lose a record than to ship
// un-redacted text to the egress path.
package fabricredactprocessor
