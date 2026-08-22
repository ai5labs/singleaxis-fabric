// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package fabricredactprocessor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap/zaptest"
)

// --- fake client used by the processor tests ---

type fakeClient struct {
	fn func(ctx context.Context, path, value string) (RedactionResult, error)
}

func (f fakeClient) Redact(ctx context.Context, path, value string) (RedactionResult, error) {
	return f.fn(ctx, path, value)
}
func (fakeClient) Close() error { return nil }

// --- UDS sidecar helpers ---

func startUDSSidecar(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fbr")
	if err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix %q: %v", sock, err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ln)
		close(done)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		<-done
		_ = os.RemoveAll(dir)
	})
	return sock
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fbr")
	if err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// --- pdata helpers ---

func makeLogsOneRecord(attrs map[string]any) plog.Logs {
	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	lr := sl.LogRecords().AppendEmpty()
	for k, v := range attrs {
		switch val := v.(type) {
		case string:
			lr.Attributes().PutStr(k, val)
		case int:
			lr.Attributes().PutInt(k, int64(val))
		case float64:
			lr.Attributes().PutDouble(k, val)
		case bool:
			lr.Attributes().PutBool(k, val)
		default:
			panic("unsupported attr type in test helper")
		}
	}
	return ld
}

func firstRecord(ld plog.Logs) (plog.LogRecord, bool) {
	if ld.ResourceLogs().Len() == 0 {
		return plog.LogRecord{}, false
	}
	rl := ld.ResourceLogs().At(0)
	if rl.ScopeLogs().Len() == 0 {
		return plog.LogRecord{}, false
	}
	records := rl.ScopeLogs().At(0).LogRecords()
	if records.Len() == 0 {
		return plog.LogRecord{}, false
	}
	return records.At(0), true
}

func recordCount(ld plog.Logs) int {
	n := 0
	for ri := 0; ri < ld.ResourceLogs().Len(); ri++ {
		rl := ld.ResourceLogs().At(ri)
		for si := 0; si < rl.ScopeLogs().Len(); si++ {
			n += rl.ScopeLogs().At(si).LogRecords().Len()
		}
	}
	return n
}

func getAttr(lr plog.LogRecord, key string) (pcommon.Value, bool) {
	return lr.Attributes().Get(key)
}

// --- config tests ---

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"default missing socket", func(c *Config) {}, true},
		{"ok", func(c *Config) { c.UnixSocket = "/tmp/s" }, false},
		{"zero timeout", func(c *Config) { c.UnixSocket = "/tmp/s"; c.Timeout = 0 }, true},
		{"empty class attr", func(c *Config) { c.UnixSocket = "/tmp/s"; c.EventClassAttribute = "" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := createDefaultConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate: err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// --- processor (fake client) tests ---

func TestProcessorHashesStringAttributes(t *testing.T) {
	client := fakeClient{fn: func(_ context.Context, path, value string) (RedactionResult, error) {
		if value == "ada@example.com" {
			return RedactionResult{Value: "HASH_EMAIL", Hashed: true, PIICategory: "EMAIL"}, nil
		}
		return RedactionResult{Value: value, Hashed: false}, nil
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored-for-fake"
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	ld := makeLogsOneRecord(map[string]any{
		"event_class": "decision_summary",
		"email":       "ada@example.com",
		"latency_ms":  12,
	})

	out, err := r.processLogs(context.Background(), ld)
	if err != nil {
		t.Fatalf("processLogs: %v", err)
	}
	if recordCount(out) != 1 {
		t.Fatalf("record count = %d", recordCount(out))
	}
	lr, _ := firstRecord(out)
	email, _ := getAttr(lr, "email")
	if email.Str() != "HASH_EMAIL" {
		t.Fatalf("email not hashed, got %q", email.Str())
	}
	latency, _ := getAttr(lr, "latency_ms")
	if latency.Int() != 12 {
		t.Fatalf("non-string attr mutated: %v", latency.Int())
	}
}

func TestProcessorAppliesTagModeReplacementWhenNotHashed(t *testing.T) {
	client := fakeClient{fn: func(_ context.Context, _, value string) (RedactionResult, error) {
		if value == "email ada@example.com" {
			return RedactionResult{Value: "email <EMAIL_ADDRESS_1>", Hashed: false}, nil
		}
		return RedactionResult{Value: value, Hashed: false}, nil
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	td := makeTracesOneSpan(map[string]any{
		"event_class": "fabric.decision",
		"prompt":      "email ada@example.com",
	})
	if _, err := r.processTraces(context.Background(), td); err != nil {
		t.Fatalf("processTraces: %v", err)
	}
	sp := td.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	prompt, _ := sp.Attributes().Get("prompt")
	if prompt.Str() != "email <EMAIL_ADDRESS_1>" {
		t.Fatalf("tag-mode replacement discarded, got %q", prompt.Str())
	}
}

func TestProcessorFailClosedOnSidecarError(t *testing.T) {
	client := fakeClient{fn: func(_ context.Context, _, _ string) (RedactionResult, error) {
		return RedactionResult{}, errors.New("sidecar down")
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	ld := makeLogsOneRecord(map[string]any{
		"event_class": "decision_summary",
		"note":        "hello",
	})
	out, err := r.processLogs(context.Background(), ld)
	if err != nil {
		t.Fatalf("processLogs: %v", err)
	}
	if recordCount(out) != 0 {
		t.Fatalf("expected record dropped, got count %d", recordCount(out))
	}
}

func TestProcessorSkipsListedAttributes(t *testing.T) {
	calls := 0
	client := fakeClient{fn: func(_ context.Context, path, value string) (RedactionResult, error) {
		calls++
		if path == "decision_summary.skipme" {
			t.Errorf("skip attribute sent to sidecar at path=%q", path)
		}
		return RedactionResult{Value: value}, nil
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	cfg.SkipAttributes = []string{"skipme"}
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	ld := makeLogsOneRecord(map[string]any{
		"event_class": "decision_summary",
		"skipme":      "secret-id",
		"body":        "free-form",
	})
	if _, err := r.processLogs(context.Background(), ld); err != nil {
		t.Fatalf("processLogs: %v", err)
	}
	// `event_class` + `body` get sent; `skipme` is skipped.
	if calls != 2 {
		t.Fatalf("expected 2 sidecar calls, got %d", calls)
	}
}

func TestProcessorEmptyStringSkipped(t *testing.T) {
	calls := 0
	client := fakeClient{fn: func(_ context.Context, _, _ string) (RedactionResult, error) {
		calls++
		return RedactionResult{}, nil
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	ld := makeLogsOneRecord(map[string]any{
		"event_class": "decision_summary",
		"empty":       "",
	})
	if _, err := r.processLogs(context.Background(), ld); err != nil {
		t.Fatalf("processLogs: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (event_class only), got %d", calls)
	}
}

// --- real client round-trip over a UDS sidecar ---

func TestClientRoundTripOverUDS(t *testing.T) {
	var (
		gotPath  string
		gotValue string
	)
	sock := startUDSSidecar(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/redact" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var req redactRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		gotPath = req.Path
		gotValue = req.Value
		_ = json.NewEncoder(w).Encode(
			map[string]any{"value": "HASH", "hashed": true, "pii_category": "EMAIL"},
		)
	}))

	c, err := NewUDSClient(sock, 2*time.Second)
	if err != nil {
		t.Fatalf("NewUDSClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	res, err := c.Redact(context.Background(), "decision_summary.note", "ada@example.com")
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if gotPath != "decision_summary.note" || gotValue != "ada@example.com" {
		t.Fatalf("sidecar got unexpected payload: %q / %q", gotPath, gotValue)
	}
	if !res.Hashed || res.Value != "HASH" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestClientNon200IsError(t *testing.T) {
	sock := startUDSSidecar(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	c, err := NewUDSClient(sock, time.Second)
	if err != nil {
		t.Fatalf("NewUDSClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.Redact(context.Background(), "a", "b"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected 502 error, got %v", err)
	}
}

func TestClientMissingValueFailsClosed(t *testing.T) {
	sock := startUDSSidecar(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"hashed": false, "pii_category": ""})
	}))
	c, err := NewUDSClient(sock, time.Second)
	if err != nil {
		t.Fatalf("NewUDSClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.Redact(context.Background(), "a", "b"); err == nil ||
		!strings.Contains(err.Error(), "missing value") {
		t.Fatalf("expected missing-value error, got %v", err)
	}
}

func TestClientUnreachableSocketFailsClosed(t *testing.T) {
	c, err := NewUDSClient(filepath.Join(shortTempDir(t), "missing"), 200*time.Millisecond)
	if err != nil {
		t.Fatalf("NewUDSClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.Redact(context.Background(), "a", "b"); err == nil {
		t.Fatal("expected transport error against missing socket")
	}
}

func TestClientValidation(t *testing.T) {
	if _, err := NewUDSClient("", time.Second); err == nil {
		t.Fatal("empty socket must error")
	}
	if _, err := NewUDSClient("/tmp/s", 0); err == nil {
		t.Fatal("zero timeout must error")
	}
}

// --- factory default config ---

func TestFactoryDefaultConfigIsInvalid(t *testing.T) {
	// The factory returns a default config that operators must fill
	// in. Validate() should flag the missing unix_socket.
	f := NewFactory()
	cfg, ok := f.CreateDefaultConfig().(*Config)
	if !ok {
		t.Fatalf("default config has unexpected type %T", f.CreateDefaultConfig())
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected default config to fail validation (no unix_socket)")
	}
}

// --- traces helpers ---

func makeTracesOneSpan(attrs map[string]any) ptrace.Traces {
	td := ptrace.NewTraces()
	rs := td.ResourceSpans().AppendEmpty()
	ss := rs.ScopeSpans().AppendEmpty()
	sp := ss.Spans().AppendEmpty()
	sp.SetName("decision")
	for k, v := range attrs {
		putAttr(sp.Attributes(), k, v)
	}
	return td
}

func putAttr(m pcommon.Map, k string, v any) {
	switch val := v.(type) {
	case string:
		m.PutStr(k, val)
	case int:
		m.PutInt(k, int64(val))
	case float64:
		m.PutDouble(k, val)
	case bool:
		m.PutBool(k, val)
	default:
		panic("unsupported attr type in test helper")
	}
}

func spanCount(td ptrace.Traces) int {
	n := 0
	for ri := 0; ri < td.ResourceSpans().Len(); ri++ {
		sss := td.ResourceSpans().At(ri).ScopeSpans()
		for si := 0; si < sss.Len(); si++ {
			n += sss.At(si).Spans().Len()
		}
	}
	return n
}

// --- traces processor tests (H-1: spans must not bypass redaction) ---

func TestTracesHashesSpanAttributes(t *testing.T) {
	client := fakeClient{fn: func(_ context.Context, _, value string) (RedactionResult, error) {
		if value == "ada@example.com" {
			return RedactionResult{Value: "HASH_EMAIL", Hashed: true}, nil
		}
		return RedactionResult{Value: value}, nil
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	td := makeTracesOneSpan(map[string]any{
		"event_class": "fabric.decision",
		"email":       "ada@example.com",
	})
	if _, err := r.processTraces(context.Background(), td); err != nil {
		t.Fatalf("processTraces: %v", err)
	}
	if spanCount(td) != 1 {
		t.Fatalf("span count = %d", spanCount(td))
	}
	sp := td.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	email, _ := sp.Attributes().Get("email")
	if email.Str() != "HASH_EMAIL" {
		t.Fatalf("span email not hashed, got %q", email.Str())
	}
}

func TestTracesFailClosedDropsSpanOnSidecarError(t *testing.T) {
	client := fakeClient{fn: func(_ context.Context, _, _ string) (RedactionResult, error) {
		return RedactionResult{}, errors.New("sidecar down")
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	td := makeTracesOneSpan(map[string]any{"event_class": "fabric.decision", "note": "hi"})
	if _, err := r.processTraces(context.Background(), td); err != nil {
		t.Fatalf("processTraces: %v", err)
	}
	if spanCount(td) != 0 {
		t.Fatalf("expected span dropped, got %d", spanCount(td))
	}
}

func TestTracesRedactsSpanEventAttributes(t *testing.T) {
	var gotPath string
	client := fakeClient{fn: func(_ context.Context, path, value string) (RedactionResult, error) {
		if value == "ada@example.com" {
			gotPath = path
			return RedactionResult{Value: "HASH_EMAIL", Hashed: true}, nil
		}
		return RedactionResult{Value: value}, nil
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	td := makeTracesOneSpan(map[string]any{"event_class": "fabric.decision"})
	ev := td.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Events().AppendEmpty()
	ev.SetName("guardrail.check")
	ev.Attributes().PutStr("raw_input", "ada@example.com")

	if _, err := r.processTraces(context.Background(), td); err != nil {
		t.Fatalf("processTraces: %v", err)
	}
	sp := td.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
	raw, _ := sp.Events().At(0).Attributes().Get("raw_input")
	if raw.Str() != "HASH_EMAIL" {
		t.Fatalf("event attr not hashed, got %q", raw.Str())
	}
	if gotPath != "fabric.decision.guardrail.check.raw_input" {
		t.Fatalf("event path = %q, want namespaced event path", gotPath)
	}
}

func TestTracesSkipsListedAttributes(t *testing.T) {
	calls := 0
	client := fakeClient{fn: func(_ context.Context, path, _ string) (RedactionResult, error) {
		calls++
		if path == "fabric.decision.skipme" {
			t.Errorf("skip attribute sent to sidecar at path=%q", path)
		}
		return RedactionResult{Value: "x"}, nil
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	cfg.SkipAttributes = []string{"skipme"}
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	td := makeTracesOneSpan(map[string]any{
		"event_class": "fabric.decision",
		"skipme":      "secret-id",
		"note":        "free-form",
	})
	if _, err := r.processTraces(context.Background(), td); err != nil {
		t.Fatalf("processTraces: %v", err)
	}
	if calls != 2 { // event_class + note; skipme skipped
		t.Fatalf("expected 2 sidecar calls, got %d", calls)
	}
}

// --- M-1 coverage: bodies, resource/scope attrs, nested values ---

func TestLogsRedactsStringBody(t *testing.T) {
	client := fakeClient{fn: func(_ context.Context, path, value string) (RedactionResult, error) {
		if value == "ada@example.com" {
			if path != "decision_summary.body" {
				t.Errorf("body path = %q", path)
			}
			return RedactionResult{Value: "HASH_BODY", Hashed: true}, nil
		}
		return RedactionResult{Value: value}, nil
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	ld := plog.NewLogs()
	lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Attributes().PutStr("event_class", "decision_summary")
	lr.Body().SetStr("ada@example.com")

	if _, err := r.processLogs(context.Background(), ld); err != nil {
		t.Fatalf("processLogs: %v", err)
	}
	rec, _ := firstRecord(ld)
	if rec.Body().Str() != "HASH_BODY" {
		t.Fatalf("log body not redacted, got %q", rec.Body().Str())
	}
}

func TestLogsRedactsResourceAndScopeAttributes(t *testing.T) {
	client := fakeClient{fn: func(_ context.Context, _, value string) (RedactionResult, error) {
		if value == "ada@example.com" {
			return RedactionResult{Value: "HASH", Hashed: true}, nil
		}
		return RedactionResult{Value: value}, nil
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	ld := plog.NewLogs()
	rl := ld.ResourceLogs().AppendEmpty()
	rl.Resource().Attributes().PutStr("service.user", "ada@example.com")
	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().Attributes().PutStr("scope.contact", "ada@example.com")
	lr := sl.LogRecords().AppendEmpty()
	lr.Attributes().PutStr("event_class", "decision_summary")

	if _, err := r.processLogs(context.Background(), ld); err != nil {
		t.Fatalf("processLogs: %v", err)
	}
	res, _ := ld.ResourceLogs().At(0).Resource().Attributes().Get("service.user")
	scope, _ := ld.ResourceLogs().At(0).ScopeLogs().At(0).Scope().Attributes().Get("scope.contact")
	if res.Str() != "HASH" || scope.Str() != "HASH" {
		t.Fatalf("resource/scope attrs not redacted: %q / %q", res.Str(), scope.Str())
	}
}

func TestRedactsNestedMapAndSliceValues(t *testing.T) {
	type call struct{ path, value string }
	var calls []call
	client := fakeClient{fn: func(_ context.Context, path, value string) (RedactionResult, error) {
		calls = append(calls, call{path, value})
		return RedactionResult{Value: "H:" + value, Hashed: true}, nil
	}}
	cfg := createDefaultConfig()
	cfg.UnixSocket = "ignored"
	r := newRedactor(cfg, client, zaptest.NewLogger(t))

	ld := plog.NewLogs()
	lr := ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	lr.Attributes().PutStr("event_class", "decision_summary")
	nested := lr.Attributes().PutEmptyMap("payload")
	nested.PutStr("inner_email", "ada@example.com")
	arr := lr.Attributes().PutEmptySlice("tags")
	arr.AppendEmpty().SetStr("bob@example.com")

	if _, err := r.processLogs(context.Background(), ld); err != nil {
		t.Fatalf("processLogs: %v", err)
	}
	rec, _ := firstRecord(ld)
	nestedOut, _ := rec.Attributes().Get("payload")
	innerEmail, _ := nestedOut.Map().Get("inner_email")
	if innerEmail.Str() != "H:ada@example.com" {
		t.Fatalf("nested map value not redacted: %v", nestedOut.AsRaw())
	}
	arrOut, _ := rec.Attributes().Get("tags")
	if arrOut.Slice().At(0).Str() != "H:bob@example.com" {
		t.Fatalf("slice value not redacted: %v", arrOut.AsRaw())
	}
	joined := make([]string, 0, len(calls))
	for _, c := range calls {
		joined = append(joined, c.path)
	}
	want := []string{
		"decision_summary.event_class",
		"decision_summary.payload.inner_email",
		"decision_summary.tags.0",
	}
	for _, w := range want {
		found := false
		for _, g := range joined {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing sidecar path %q; got %v", w, joined)
		}
	}
}
