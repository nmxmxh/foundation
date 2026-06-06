package logger

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	metautil "github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
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
