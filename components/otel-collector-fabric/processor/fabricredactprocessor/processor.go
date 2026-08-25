// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package fabricredactprocessor

import (
	"context"
	"strconv"
	"unicode/utf8"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"
)

type redactor struct {
	cfg    *Config
	client Client
	logger *zap.Logger
	skip   map[string]struct{}
}

func newRedactor(cfg *Config, client Client, logger *zap.Logger) *redactor {
	skip := make(map[string]struct{}, len(cfg.SkipAttributes))
	for _, k := range cfg.SkipAttributes {
		skip[k] = struct{}{}
	}
	return &redactor{cfg: cfg, client: client, logger: logger, skip: skip}
}

// processLogs redacts every string reachable in the batch: log
// bodies, severity text, record attributes, scope metadata and
// attributes, resource schema URLs and attributes — recursing into
// map and slice values so content leaves never bypass the sidecar.
// Anything that hits a sidecar error is dropped together with its
// whole scope or resource (fail-closed).
//
// skip_attributes applies to top-level attribute keys only; keys
// nested inside map values are always treated as content and sent to
// the sidecar. Attribute keys are structural identifiers and are not
// renamed; their values are redacted.
func (r *redactor) processLogs(ctx context.Context, ld plog.Logs) (plog.Logs, error) {
	rls := ld.ResourceLogs()
	for ri := 0; ri < rls.Len(); ri++ {
		rl := rls.At(ri)
		resOK := r.redactProtocolString(ctx, "resource.schema_url", rl.SchemaUrl(), rl.SetSchemaUrl) &&
			r.redactAttrMap(ctx, "resource", rl.Resource().Attributes())
		sls := rl.ScopeLogs()
		for si := 0; si < sls.Len(); si++ {
			sl := sls.At(si)
			scopeOK := resOK && r.redactScope(ctx, sl.Scope()) &&
				r.redactProtocolString(ctx, "scope.schema_url", sl.SchemaUrl(), sl.SetSchemaUrl) &&
				r.redactAttrMap(ctx, "scope", sl.Scope().Attributes())
			sl.LogRecords().RemoveIf(func(lr plog.LogRecord) bool {
				return !(scopeOK && r.redactLogRecord(ctx, lr))
			})
		}
	}
	return ld, nil
}

// processTraces applies the identical policy to spans: span
// attributes, span/event names, status messages, trace state, event
// and link attributes, scope metadata and resource metadata. A failing
// sidecar drops the span (and, for resource/scope failures, every span
// under them).
func (r *redactor) processTraces(ctx context.Context, td ptrace.Traces) (ptrace.Traces, error) {
	rss := td.ResourceSpans()
	for ri := 0; ri < rss.Len(); ri++ {
		rs := rss.At(ri)
		resOK := r.redactProtocolString(ctx, "resource.schema_url", rs.SchemaUrl(), rs.SetSchemaUrl) &&
			r.redactAttrMap(ctx, "resource", rs.Resource().Attributes())
		sss := rs.ScopeSpans()
		for si := 0; si < sss.Len(); si++ {
			ss := sss.At(si)
			scopeOK := resOK && r.redactScope(ctx, ss.Scope()) &&
				r.redactProtocolString(ctx, "scope.schema_url", ss.SchemaUrl(), ss.SetSchemaUrl) &&
				r.redactAttrMap(ctx, "scope", ss.Scope().Attributes())
			ss.Spans().RemoveIf(func(sp ptrace.Span) bool {
				return !(scopeOK && r.redactSpan(ctx, sp))
			})
		}
	}
	return td, nil
}

// redactLogRecord returns true when the record should be kept, false
// when it should be dropped (sidecar failure).
func (r *redactor) redactLogRecord(ctx context.Context, lr plog.LogRecord) bool {
	attrs := lr.Attributes()
	class := strAttr(attrs, r.cfg.EventClassAttribute)

	// The free-form body is exactly where prompt/completion text and
	// pasted PII end up, so it is always considered content. It is
	// namespaced under "<class>.body" (or "body" when classless) for
	// the sidecar's classification rules.
	if !r.redactValue(ctx, joinPath(class, "body"), lr.Body()) {
		return false
	}
	if !r.redactString(ctx, joinPath(class, "severity_text"), lr.SeverityText(), lr.SetSeverityText) {
		return false
	}
	return r.redactAttrMap(ctx, class, attrs)
}

// redactSpan mirrors redactLogRecord for spans, plus span events
// (guardrail verdicts, retrieval records — the highest-content
// surfaces in a Fabric trace) and links.
func (r *redactor) redactSpan(ctx context.Context, sp ptrace.Span) bool {
	attrs := sp.Attributes()
	class := strAttr(attrs, r.cfg.EventClassAttribute)
	if !r.redactString(ctx, joinPath(class, "span.name"), sp.Name(), sp.SetName) {
		return false
	}
	if !r.redactProtocolString(ctx, joinPath(class, "span.trace_state"), sp.TraceState().AsRaw(), sp.TraceState().FromRaw) {
		return false
	}
	status := sp.Status()
	if !r.redactString(ctx, joinPath(class, "status.message"), status.Message(), status.SetMessage) {
		return false
	}
	if !r.redactAttrMap(ctx, class, attrs) {
		return false
	}
	events := sp.Events()
	for i := 0; i < events.Len(); i++ {
		ev := events.At(i)
		eventName := ev.Name()
		if !r.redactString(ctx, joinPath(class, "event.name"), eventName, ev.SetName) ||
			!r.redactAttrMap(ctx, joinPath(class, eventName), ev.Attributes()) {
			return false
		}
	}
	links := sp.Links()
	for i := 0; i < links.Len(); i++ {
		link := links.At(i)
		if !r.redactProtocolString(ctx, joinPath(class, "link.trace_state"), link.TraceState().AsRaw(), link.TraceState().FromRaw) ||
			!r.redactAttrMap(ctx, joinPath(class, "link"), link.Attributes()) {
			return false
		}
	}
	return true
}

// redactScope treats scope name and version as structural metadata but still
// inspects them. A normal identifier is returned unchanged by the sidecar;
// producer-supplied PII is replaced. This preserves grouping without trusting
// every producer to use these fields correctly.
func (r *redactor) redactScope(ctx context.Context, scope pcommon.InstrumentationScope) bool {
	return r.redactString(ctx, "scope.name", scope.Name(), scope.SetName) &&
		r.redactString(ctx, "scope.version", scope.Version(), scope.SetVersion)
}

// redactString sends a pdata string field to the sidecar and applies its
// authoritative response. Empty strings contain no content and are skipped.
func (r *redactor) redactString(ctx context.Context, path, value string, set func(string)) bool {
	if value == "" {
		return true
	}
	res, err := r.client.Redact(ctx, path, value)
	if err != nil {
		r.logFailure(err)
		return false
	}
	set(res.Value)
	return true
}

// redactProtocolString inspects a structural string whose grammar could be
// invalidated by a replacement (schema URLs and W3C trace state). Safe values
// survive byte-for-byte. If the sidecar changes the value, the field is cleared
// rather than exporting malformed or sensitive protocol metadata.
func (r *redactor) redactProtocolString(ctx context.Context, path, value string, set func(string)) bool {
	if value == "" {
		return true
	}
	res, err := r.client.Redact(ctx, path, value)
	if err != nil {
		r.logFailure(err)
		return false
	}
	if res.Value != value {
		set("")
	}
	return true
}

// redactAttrMap walks one attribute map, applying the skip-list to
// top-level keys only, and redacts each value recursively. Values are
// snapshotted before mutation so we never mutate while ranging.
// Returns false on sidecar failure (caller drops the payload).
func (r *redactor) redactAttrMap(ctx context.Context, prefix string, m pcommon.Map) bool {
	type entry struct {
		key string
		val pcommon.Value
	}
	entries := make([]entry, 0, m.Len())
	m.Range(func(k string, v pcommon.Value) bool {
		entries = append(entries, entry{key: k, val: v})
		return true
	})
	for _, e := range entries {
		if _, skipped := r.skip[e.key]; skipped {
			continue
		}
		if !r.inspectStructuralKey(ctx, joinPath(prefix, "attribute_key"), e.key) {
			return false
		}
		if !r.redactValue(ctx, joinPath(prefix, e.key), e.val) {
			return false
		}
	}
	return true
}

// inspectStructuralKey validates a map key without renaming it. Renaming keys
// can collapse two fields and corrupt evidence semantics, so a key that the
// sidecar changes causes the containing payload to be dropped. Safe schema
// identifiers remain byte-for-byte stable.
func (r *redactor) inspectStructuralKey(ctx context.Context, path, key string) bool {
	if key == "" {
		return true
	}
	res, err := r.client.Redact(ctx, path, key)
	if err != nil {
		r.logFailure(err)
		return false
	}
	if res.Value != key {
		r.logger.Warn("sensitive structural key — dropping payload")
		return false
	}
	return true
}

// redactValue redacts a single attribute value in place, recursing
// through maps and slices down to their string leaves. Returns false
// on sidecar failure.
func (r *redactor) redactValue(ctx context.Context, path string, v pcommon.Value) bool {
	switch v.Type() {
	case pcommon.ValueTypeStr:
		return r.redactString(ctx, path, v.Str(), v.SetStr)
	case pcommon.ValueTypeBytes:
		b := v.Bytes()
		if b.Len() == 0 || r.cfg.effectiveByteHandling() == ByteHandlingPassthrough {
			return true
		}
		if r.cfg.effectiveByteHandling() == ByteHandlingReject || !utf8.Valid(b.AsRaw()) {
			r.logger.Warn("uninspectable byte content — dropping payload",
				zap.String("byte_handling", r.cfg.effectiveByteHandling()))
			return false
		}
		return r.redactString(ctx, path, string(b.AsRaw()), func(redacted string) {
			b.FromRaw([]byte(redacted))
		})
	case pcommon.ValueTypeInt:
		// Numeric attributes sometimes carry account, phone, or government
		// identifiers. Inspect their canonical representation but keep the
		// original type when it is safe; a detected replacement cannot be
		// represented losslessly as an integer, so fail closed.
		return r.inspectScalar(ctx, path, strconv.FormatInt(v.Int(), 10))
	case pcommon.ValueTypeDouble:
		return r.inspectScalar(ctx, path, strconv.FormatFloat(v.Double(), 'g', -1, 64))
	case pcommon.ValueTypeMap:
		m := v.Map()
		keys := make([]string, 0, m.Len())
		m.Range(func(k string, _ pcommon.Value) bool {
			keys = append(keys, k)
			return true
		})
		for _, k := range keys {
			if !r.inspectStructuralKey(ctx, joinPath(path, "map_key"), k) {
				return false
			}
			child, _ := m.Get(k)
			if !r.redactValue(ctx, joinPath(path, k), child) {
				return false
			}
		}
		return true
	case pcommon.ValueTypeSlice:
		s := v.Slice()
		for i := 0; i < s.Len(); i++ {
			if !r.redactValue(ctx, joinPath(path, strconv.Itoa(i)), s.At(i)) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

func (r *redactor) inspectScalar(ctx context.Context, path, value string) bool {
	res, err := r.client.Redact(ctx, path, value)
	if err != nil {
		r.logFailure(err)
		return false
	}
	if res.Value != value {
		r.logger.Warn("sensitive typed scalar — dropping payload")
		return false
	}
	return true
}

// logFailure intentionally omits path and content. Both can be controlled by a
// telemetry producer and may themselves contain PII or secrets.
func (r *redactor) logFailure(err error) {
	// Do not log err: transport/protocol implementations are not allowed to
	// turn an echoed request or producer-controlled socket detail into a second
	// telemetry leak. The generic warning records the occurrence; detailed
	// diagnostics remain at the sidecar boundary.
	_ = err
	r.logger.Warn("redaction sidecar error — dropping payload")
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func strAttr(attrs pcommon.Map, key string) string {
	v, ok := attrs.Get(key)
	if !ok || v.Type() != pcommon.ValueTypeStr {
		return ""
	}
	return v.Str()
}
