// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package fabricredactprocessor

import (
	"context"
	"strconv"

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
// bodies, record attributes, scope attributes, and resource
// attributes — recursing into map and slice values so string leaves
// never bypass the sidecar. Anything that hits a sidecar error is
// dropped together with its whole scope or resource (fail-closed).
//
// skip_attributes applies to top-level attribute keys only; keys
// nested inside map values are always treated as content and sent to
// the sidecar. Bytes-valued attributes are not redacted (the sidecar
// wire contract is strings-only); they are passed through untouched.
func (r *redactor) processLogs(ctx context.Context, ld plog.Logs) (plog.Logs, error) {
	rls := ld.ResourceLogs()
	for ri := 0; ri < rls.Len(); ri++ {
		rl := rls.At(ri)
		resOK := r.redactAttrMap(ctx, "resource", rl.Resource().Attributes())
		sls := rl.ScopeLogs()
		for si := 0; si < sls.Len(); si++ {
			sl := sls.At(si)
			scopeOK := resOK && r.redactAttrMap(ctx, "scope", sl.Scope().Attributes())
			sl.LogRecords().RemoveIf(func(lr plog.LogRecord) bool {
				return !(scopeOK && r.redactLogRecord(ctx, lr))
			})
		}
	}
	return ld, nil
}

// processTraces applies the identical policy to spans: span
// attributes, span-event attributes, link attributes, scope and
// resource attributes. A failing sidecar drops the span (and, for
// resource/scope failures, every span under them).
func (r *redactor) processTraces(ctx context.Context, td ptrace.Traces) (ptrace.Traces, error) {
	rss := td.ResourceSpans()
	for ri := 0; ri < rss.Len(); ri++ {
		rs := rss.At(ri)
		resOK := r.redactAttrMap(ctx, "resource", rs.Resource().Attributes())
		sss := rs.ScopeSpans()
		for si := 0; si < sss.Len(); si++ {
			ss := sss.At(si)
			scopeOK := resOK && r.redactAttrMap(ctx, "scope", ss.Scope().Attributes())
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
	if body := lr.Body(); body.Type() == pcommon.ValueTypeStr {
		if !r.redactValue(ctx, joinPath(class, "body"), body) {
			return false
		}
	}
	return r.redactAttrMap(ctx, class, attrs)
}

// redactSpan mirrors redactLogRecord for spans, plus span events
// (guardrail verdicts, retrieval records — the highest-content
// surfaces in a Fabric trace) and links.
func (r *redactor) redactSpan(ctx context.Context, sp ptrace.Span) bool {
	attrs := sp.Attributes()
	class := strAttr(attrs, r.cfg.EventClassAttribute)
	if !r.redactAttrMap(ctx, class, attrs) {
		return false
	}
	events := sp.Events()
	for i := 0; i < events.Len(); i++ {
		ev := events.At(i)
		if !r.redactAttrMap(ctx, joinPath(class, ev.Name()), ev.Attributes()) {
			return false
		}
	}
	links := sp.Links()
	for i := 0; i < links.Len(); i++ {
		if !r.redactAttrMap(ctx, joinPath(class, "link"), links.At(i).Attributes()) {
			return false
		}
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
		if !r.redactValue(ctx, joinPath(prefix, e.key), e.val) {
			return false
		}
	}
	return true
}

// redactValue redacts a single attribute value in place, recursing
// through maps and slices down to their string leaves. Returns false
// on sidecar failure.
func (r *redactor) redactValue(ctx context.Context, path string, v pcommon.Value) bool {
	switch v.Type() {
	case pcommon.ValueTypeStr:
		s := v.Str()
		if s == "" {
			return true
		}
		res, err := r.client.Redact(ctx, path, s)
		if err != nil {
			r.logger.Warn("redaction sidecar error — dropping payload",
				zap.String("path", path), zap.Error(err))
			return false
		}
		if res.Hashed {
			v.SetStr(res.Value)
		}
		return true
	case pcommon.ValueTypeMap:
		m := v.Map()
		keys := make([]string, 0, m.Len())
		m.Range(func(k string, _ pcommon.Value) bool {
			keys = append(keys, k)
			return true
		})
		for _, k := range keys {
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
