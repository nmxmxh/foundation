package logger

import (
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestLoggerConstructorsAndFiltering(t *testing.T) {
	if DefaultConfig().ServiceName == "" || ProductionConfig().Environment != "production" {
		t.Fatal("unexpected default configs")
	}
	dev, err := NewDefault()
	if err != nil {
		t.Fatalf("NewDefault() error = %v", err)
	}
	dev.Info("info", zap.String("component", "test"))
	dev.Debug("debug")
	dev.Warn("warn")
	dev.Error("error")
	_ = dev.Sync()
	if dev.With(zap.String("scope", "child")).GetZapLogger() == nil {
		t.Fatal("expected child zap logger")
	}
	production, err := NewProduction("svc-prod")
	if err != nil {
		t.Fatalf("NewProduction() error = %v", err)
	}
	if production.GetZapLogger() == nil {
		t.Fatalf("expected production zap logger")
	}

	prod, err := New(Config{
		Environment:     "production",
		LogLevel:        "info",
		ServiceName:     "svc",
		EnableFiltering: true,
		FilterInterval:  5,
		MaxSimilarLogs:  1,
	})
	if err != nil {
		t.Fatalf("New(production) error = %v", err)
	}
	typed := prod.(*logger)
	if !typed.shouldLog("same", "info") {
		t.Fatal("first similar log should pass")
	}
	if typed.shouldLog("same", "info") {
		t.Fatal("second similar log should be filtered")
	}
	time.Sleep(10 * time.Millisecond)
	if !typed.shouldLog("same", "info") {
		t.Fatal("log should pass after interval")
	}
	if parseLogLevel("debug") != zapcore.DebugLevel || parseLogLevel("warn") != zapcore.WarnLevel || parseLogLevel("error") != zapcore.ErrorLevel || parseLogLevel("bad") != zapcore.InfoLevel {
		t.Fatal("parseLogLevel failed")
	}
	if GetCaller(0) == "unknown" {
		t.Fatal("expected caller")
	}
}

func TestBeautifulEncoder(t *testing.T) {
	encoder := NewBeautifulEncoder(zapcore.EncoderConfig{})
	buf, err := encoder.EncodeEntry(zapcore.Entry{Level: zapcore.InfoLevel, Message: strings.Repeat("m", 60), Time: time.Now()}, []zapcore.Field{
		zap.String("component", "component-with-a-very-long-name"),
		zap.String("service", "svc"),
		zap.String("correlation_id", "123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("EncodeEntry() error = %v", err)
	}
	if !strings.Contains(buf.String(), "INF") {
		t.Fatalf("encoded line missing level: %q", buf.String())
	}
	buf.Free()
	if encoder.Clone() == nil {
		t.Fatal("expected clone")
	}
	for _, level := range []zapcore.Level{zapcore.DebugLevel, zapcore.InfoLevel, zapcore.WarnLevel, zapcore.ErrorLevel, zapcore.DPanicLevel} {
		if formatLevel(level) == "" {
			t.Fatalf("empty level for %s", level)
		}
	}
}
