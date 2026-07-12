package logger

import (
	"bytes"
	"context"
	"errors"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	metautil "github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoggerConstructorsAndFiltering(t *testing.T) {
	if DefaultConfig().ServiceName == "" || ProductionConfig().Environment != "production" {
		t.Fatal("unexpected default configs")
	}
	var devOut bytes.Buffer
	dev, err := New(Config{
		Environment: "development",
		LogLevel:    "debug",
		ServiceName: "svc-dev",
		Component:   "test",
		Output:      &devOut,
	})
	if err != nil {
		t.Fatalf("New() dev error = %v", err)
	}
	dev.Info("info", "component", "unit")
	dev.Debug("debug")
	dev.Warn("warn")
	dev.Error("error")
	if dev.With("scope", "child") == nil {
		t.Fatal("expected child logger")
	}
	if got := devOut.String(); !strings.Contains(got, "INF") || !strings.Contains(got, "\033[") {
		t.Fatalf("development output missing level/color: %q", got)
	}

	var prodOut bytes.Buffer
	prod, err := New(Config{
		Environment:     "production",
		LogLevel:        "info",
		ServiceName:     "svc-prod",
		Component:       "test",
		Output:          &prodOut,
		EnableFiltering: true,
		FilterInterval:  50,
		MaxSimilarLogs:  1,
	})
	if err != nil {
		t.Fatalf("New() production error = %v", err)
	}
	prod.Info("same")
	prod.Info("same")
	if got := strings.Count(prodOut.String(), `"message":"same"`); got != 1 {
		t.Fatalf("expected one filtered production log, got %d in %q", got, prodOut.String())
	}
	time.Sleep(60 * time.Millisecond)
	prod.Info("same")
	if got := strings.Count(prodOut.String(), `"message":"same"`); got != 2 {
		t.Fatalf("expected log after filter interval, got %d in %q", got, prodOut.String())
	}
	if parseLogLevel("debug") != slog.LevelDebug || parseLogLevel("warn") != slog.LevelWarn || parseLogLevel("error") != slog.LevelError || parseLogLevel("bad") != slog.LevelInfo {
		t.Fatal("parseLogLevel failed")
	}
	if GetCaller(0) == "unknown" {
		t.Fatal("expected caller")
	}
}

func TestRuntimeConfigAndInstallDeclareApplicationScope(t *testing.T) {
	var out bytes.Buffer
	cfg := RuntimeConfig("development", "info", "docuos", "startup")
	cfg.Output = &out
	cfg.DisableANSI = true
	l := Install(cfg)
	l.Info("started")
	got := out.String()
	if !strings.Contains(got, "[docuos/startup]") || strings.Contains(got, "ovasabi-service/foundation") {
		t.Fatalf("runtime scope not applied: %q", got)
	}
	if Default() == nil {
		t.Fatal("expected installed default logger")
	}
}

func TestLoggerRedactsAndEnrichesContext(t *testing.T) {
	var out bytes.Buffer
	l, err := New(Config{
		Environment: "production",
		LogLevel:    "info",
		ServiceName: "svc",
		Component:   "security",
		Output:      &out,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := metautil.IntoContext(context.Background(), metautil.EnvelopeMetadata{
		CorrelationID: "corr_1",
		RequestID:     "req_1",
		TraceID:       "trace_1",
		GlobalContext: &metautil.GlobalContext{
			OrganizationID: "org_1",
			UserID:         "actor_1",
		},
		Attributes: map[string]string{
			"event_type": "media:upload:success",
			"projection": "media_latest",
		},
		Extras: extension.Object{"epoch": extension.Int(42)},
	})
	l.InfoContext(ctx, "security boundary checked", "password", "super-secret", "authorization", "Bearer token", "media_id", "m_1")
	got := out.String()
	for _, want := range []string{`"correlation_id":"corr_1"`, `"request_id":"req_1"`, `"trace_id":"trace_1"`, `"organization_id":"org_1"`, `"actor_id":"actor_1"`, `"event_type":"media:upload:success"`, `"projection":"media_latest"`, `"epoch":42`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing context field %s in %q", want, got)
		}
	}
	if strings.Contains(got, "super-secret") || strings.Contains(got, "Bearer token") {
		t.Fatalf("sensitive value leaked in %q", got)
	}
	if !strings.Contains(got, `"password":"[REDACTED]"`) || !strings.Contains(got, `"authorization":"[REDACTED]"`) {
		t.Fatalf("redaction markers missing in %q", got)
	}
}

func TestLoggerCWFFormatIsRedactedAndParseable(t *testing.T) {
	var out bytes.Buffer
	l, err := New(Config{
		Environment: "production",
		LogLevel:    "info",
		Format:      FormatCWF,
		ServiceName: "svc",
		Component:   "telemetry",
		Output:      &out,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := metautil.IntoContext(context.Background(), metautil.EnvelopeMetadata{
		CorrelationID: "corr_cwf",
		Attributes:    map[string]string{"event_type": "media:upload:success"},
	})
	l.InfoContext(ctx, "uploaded\nfile", "authorization", "Bearer token", "media_id", "m=1")
	got := out.String()
	for _, want := range []string{
		"cwf.v1\t",
		"\tinfo\tsvc\ttelemetry\tuploaded\\nfile",
		"\tcorrelation_id=corr_cwf",
		"\tevent_type=media:upload:success",
		"\tmedia_id=m\\=1",
		"\tauthorization=[REDACTED]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "Bearer token") {
		t.Fatalf("sensitive value leaked in %q", got)
	}
}

func TestAsyncDropsWhenQueueIsFull(t *testing.T) {
	var sink blockingWriter
	l, err := New(Config{
		Environment: "production",
		LogLevel:    "info",
		ServiceName: "svc",
		Component:   "async",
		Output:      &sink,
		Async:       true,
		QueueDepth:  1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for i := range 128 {
		l.Info("hot log", "i", i)
	}
	if l.Dropped() == 0 {
		t.Fatal("expected dropped logs when async queue is full")
	}
}

type blockingWriter struct{}

func (blockingWriter) Write(_ []byte) (int, error) {
	select {}
}

type syncableBuffer struct {
	bytes.Buffer
	synced bool
}

func (s *syncableBuffer) Sync() error {
	s.synced = true
	return nil
}

func TestNewDefaultAndNewProduction(t *testing.T) {
	l, err := NewDefault()
	if err != nil || l == nil {
		t.Fatalf("NewDefault() = %v, %v", l, err)
	}
	p, err := NewProduction("svc-prod")
	if err != nil || p == nil {
		t.Fatalf("NewProduction() = %v, %v", p, err)
	}
	if p.Dropped() != 0 {
		t.Fatalf("Dropped() = %d before any logging", p.Dropped())
	}
	if l.Dropped() != 0 {
		t.Fatalf("sync logger Dropped() = %d", l.Dropped())
	}
	if zero, err := New(Config{}); err != nil || zero == nil {
		t.Fatalf("New(zero config) = %v, %v", zero, err)
	}
	prod := RuntimeConfig("production", "info", "svc", "runtime")
	if !prod.Async || prod.Environment != "production" {
		t.Fatalf("RuntimeConfig(production) = %+v", prod)
	}
}

func TestContextLevelMethodsAndSync(t *testing.T) {
	out := &syncableBuffer{}
	l, err := New(Config{
		Environment: "development",
		LogLevel:    "debug",
		Format:      FormatJSON,
		ServiceName: "svc",
		Component:   "ctx",
		Output:      out,
		Async:       true,
		QueueDepth:  32,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	l.DebugContext(ctx, "dbg message")
	l.WarnContext(ctx, "wrn message")
	l.ErrorContext(ctx, "err message")
	var nilCtx context.Context
	l.InfoContext(nilCtx, "nil ctx message")
	if err := l.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !out.synced {
		t.Fatal("Sync() did not reach output syncer")
	}
	got := out.String()
	for _, want := range []string{"dbg message", "wrn message", "err message", "nil ctx message"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}

	quiet, err := New(Config{LogLevel: "error", Format: FormatJSON, Output: out})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	quiet.Debug("suppressed")
	if strings.Contains(out.String(), "suppressed") {
		t.Fatal("disabled level should not log")
	}
	if err := quiet.Sync(); err != nil {
		t.Fatalf("Sync() without async error = %v", err)
	}
}

func TestExplicitConsoleFormat(t *testing.T) {
	var out bytes.Buffer
	l, err := New(Config{
		Environment: "production",
		Format:      FormatConsole,
		ServiceName: "svc",
		Component:   "console",
		Output:      &out,
		DisableANSI: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	l.Info("console line", "key", "value with space")
	got := out.String()
	if !strings.Contains(got, "console line") || !strings.Contains(got, `key="value with space"`) {
		t.Fatalf("console output = %q", got)
	}
}

func TestWithGroupVariants(t *testing.T) {
	hcfg := handlerConfig{serviceName: "svc", component: "grp", maxAttrs: 8, maxValueBytes: 256}
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "grouped message", 0)
	rec.AddAttrs(slog.String("k", "v"))

	var consoleOut bytes.Buffer
	console := newConsoleHandler(&consoleOut, slog.LevelInfo, hcfg, false).WithGroup("g1")
	if err := console.Handle(context.Background(), rec); err != nil {
		t.Fatalf("console Handle() error = %v", err)
	}
	if got := consoleOut.String(); !strings.Contains(got, "grouped message") || !strings.Contains(got, "k=v") {
		t.Fatalf("console grouped output = %q", got)
	}

	var cwfOut bytes.Buffer
	cwf := newCWFHandler(&cwfOut, slog.LevelInfo, hcfg).WithGroup("g2")
	if err := cwf.Handle(context.Background(), rec); err != nil {
		t.Fatalf("cwf Handle() error = %v", err)
	}
	if got := cwfOut.String(); !strings.HasPrefix(got, "cwf.v1\t") {
		t.Fatalf("cwf grouped output = %q", got)
	}

	var fndOut bytes.Buffer
	foundation := newFoundationHandler(newConsoleHandler(&fndOut, slog.LevelInfo, hcfg, false), hcfg).WithGroup("g3")
	if err := foundation.Handle(context.Background(), rec); err != nil {
		t.Fatalf("foundation Handle() error = %v", err)
	}

	var asyncOut bytes.Buffer
	asyncH, state := newAsyncHandler(newConsoleHandler(&asyncOut, slog.LevelInfo, hcfg, false), 4)
	grouped := asyncH.WithGroup("g4")
	if !grouped.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("async grouped handler should be enabled")
	}
	if err := grouped.Handle(context.Background(), rec); err != nil {
		t.Fatalf("async Handle() error = %v", err)
	}
	if !state.flush(time.Second) {
		t.Fatal("async flush timed out")
	}
	if got := asyncOut.String(); !strings.Contains(got, "grouped message") {
		t.Fatalf("async grouped output = %q", got)
	}
}

func TestAsyncFlushEdges(t *testing.T) {
	var nilState *asyncState
	if !nilState.flush(time.Millisecond) {
		t.Fatal("nil asyncState flush should succeed")
	}
	stuck := &asyncState{queue: make(chan logEntry)}
	if stuck.flush(10 * time.Millisecond) {
		t.Fatal("flush without consumer should time out")
	}
}

func TestFormatValueAndQuoting(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := formatValue(slog.StringValue("plain")); got != "plain" {
		t.Fatalf("formatValue(plain) = %q", got)
	}
	if got := formatValue(slog.StringValue("has space")); got != `"has space"` {
		t.Fatalf("formatValue(space) = %q", got)
	}
	if got := formatValue(slog.DurationValue(time.Second)); got != "1s" {
		t.Fatalf("formatValue(duration) = %q", got)
	}
	if got := formatValue(slog.TimeValue(ts)); got != ts.Format(time.RFC3339Nano) {
		t.Fatalf("formatValue(time) = %q", got)
	}
	if got := formatValue(slog.IntValue(42)); got != "42" {
		t.Fatalf("formatValue(int) = %q", got)
	}
	if got := quoteIfNeeded(""); got != `""` {
		t.Fatalf("quoteIfNeeded(empty) = %q", got)
	}
	if got := quoteIfNeeded("a|b"); got != `"a|b"` {
		t.Fatalf("quoteIfNeeded(pipe) = %q", got)
	}
	if got := quoteIfNeeded("clean"); got != "clean" {
		t.Fatalf("quoteIfNeeded(clean) = %q", got)
	}
}

func TestTrimMiddleAndCWFHelpers(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := trimMiddle("abc", 0); got != "abc" {
		t.Fatalf("trimMiddle(0) = %q", got)
	}
	if got := trimMiddle("abcdef", 3); got != "abc" {
		t.Fatalf("trimMiddle(3) = %q", got)
	}
	if got := trimMiddle("abcdefghij", 7); got != "ab...ij" {
		t.Fatalf("trimMiddle(7) = %q", got)
	}
	if got := trimMiddle("short", 10); got != "short" {
		t.Fatalf("trimMiddle(long max) = %q", got)
	}
	if got := cwfValue(slog.StringValue("x")); got != "x" {
		t.Fatalf("cwfValue(string) = %q", got)
	}
	if got := cwfValue(slog.DurationValue(2 * time.Second)); got != "2s" {
		t.Fatalf("cwfValue(duration) = %q", got)
	}
	if got := cwfValue(slog.TimeValue(ts)); got != ts.Format(time.RFC3339Nano) {
		t.Fatalf("cwfValue(time) = %q", got)
	}
	if got := cwfValue(slog.IntValue(7)); got != "7" {
		t.Fatalf("cwfValue(int) = %q", got)
	}
	if levelValue(slog.LevelDebug) != "debug" || levelValue(slog.LevelError) != "error" ||
		levelValue(slog.LevelWarn) != "warn" || levelValue(slog.LevelInfo) != "info" {
		t.Fatal("levelValue mismatch")
	}
	if got := escapeCWFKey(""); got != "_" {
		t.Fatalf("escapeCWFKey(empty) = %q", got)
	}
	if got := escapeCWFKey(" a=b "); got != `a\=b` {
		t.Fatalf("escapeCWFKey(equals) = %q", got)
	}
}

func TestSourcePathAndSanitizers(t *testing.T) {
	if got := sourcePath(0); got != "" {
		t.Fatalf("sourcePath(0) = %q", got)
	}
	var pcs [1]uintptr
	runtime.Callers(1, pcs[:])
	if got := sourcePath(pcs[0]); !strings.Contains(got, ".go:") {
		t.Fatalf("sourcePath(pc) = %q", got)
	}
	if got := sanitizeString("password=hunter2", 100); got != redactedValue {
		t.Fatalf("sanitizeString(sensitive) = %q", got)
	}
	long := strings.Repeat("a", 50)
	if got := sanitizeString(long, 10); got != long[:10]+"..." {
		t.Fatalf("sanitizeString(long) = %q", got)
	}
	attr := boundAttr(slog.Any("err", errors.New("boom")), 100)
	if attr.Value.String() != "boom" {
		t.Fatalf("boundAttr(error) = %q", attr.Value.String())
	}
	if GetCaller(1000) != "unknown" {
		t.Fatal("GetCaller(deep) should be unknown")
	}
}

func TestDiscriminatorAndFilterKey(t *testing.T) {
	empty := slog.NewRecord(time.Now(), slog.LevelInfo, "registered", 0)
	if got := discriminator(empty); got != "" {
		t.Fatalf("discriminator(no attrs) = %q", got)
	}
	plain := slog.NewRecord(time.Now(), slog.LevelInfo, "registered", 0)
	plain.AddAttrs(slog.String("other", "x"))
	if got := discriminator(plain); got != "" {
		t.Fatalf("discriminator(no keys) = %q", got)
	}
	tagged := slog.NewRecord(time.Now(), slog.LevelInfo, "registered", 0)
	tagged.AddAttrs(slog.String("event_type", "orders:create"), slog.String("path", "/x"))
	if got := discriminator(tagged); got != "event_type=orders:create,path=/x" {
		t.Fatalf("discriminator(tagged) = %q", got)
	}
	if got := filterKey(slog.LevelInfo, "registered", tagged); !strings.Contains(got, "|event_type=") {
		t.Fatalf("filterKey(tagged) = %q", got)
	}
	long := strings.Repeat("m", 200)
	if got := filterKey(slog.LevelInfo, long, empty); len(got) != 160 {
		t.Fatalf("filterKey(long) length = %d", len(got))
	}
}

func TestShouldLogFilteringWindow(t *testing.T) {
	h := &foundationHandler{
		config: handlerConfig{
			environment:     "production",
			enableFiltering: true,
			filterInterval:  time.Hour,
			maxSimilarLogs:  2,
		},
		filters: &filterState{cache: map[string]*logFilter{}},
	}
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
	if !h.shouldLog(slog.LevelInfo, "msg", rec) {
		t.Fatal("first log should pass")
	}
	if !h.shouldLog(slog.LevelInfo, "msg", rec) {
		t.Fatal("second log should pass under maxSimilarLogs=2")
	}
	if h.shouldLog(slog.LevelInfo, "msg", rec) {
		t.Fatal("third log should be filtered")
	}
	for _, filter := range h.filters.cache {
		filter.lastLogTime = time.Now().Add(-2 * time.Hour)
	}
	if !h.shouldLog(slog.LevelInfo, "msg", rec) {
		t.Fatal("log after interval should pass")
	}
	if !h.shouldLog(slog.LevelError, "msg", rec) {
		t.Fatal("errors must never be filtered")
	}
}

func TestDefaultFallbackAndSetDefaultNil(t *testing.T) {
	prev := Default()
	defer SetDefault(prev)
	SetDefault(nil)
	defaultLogger.Store(defaultLoggerHolder{})
	l := Default()
	if l == nil {
		t.Fatal("expected fallback logger")
	}
	l.Info("discarded")
}

func TestAddContextSkipsExistingKeys(t *testing.T) {
	var out bytes.Buffer
	hcfg := handlerConfig{serviceName: "svc", component: "ctx", maxAttrs: 8, maxValueBytes: 256}
	h := newFoundationHandler(slog.NewJSONHandler(&out, nil), hcfg)
	rec := slog.NewRecord(time.Now(), slog.LevelInfo, "dup", 0)
	rec.AddAttrs(slog.String("correlation_id", "explicit"))
	ctx := metautil.IntoContext(context.Background(), metautil.EnvelopeMetadata{CorrelationID: "from_ctx"})
	if err := h.Handle(ctx, rec); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"correlation_id":"explicit"`) || strings.Contains(got, "from_ctx") {
		t.Fatalf("record attr should win over context: %q", got)
	}
}
