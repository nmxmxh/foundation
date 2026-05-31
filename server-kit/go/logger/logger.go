package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	metautil "github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

const (
	defaultFilterInterval = 2 * time.Second
	defaultMaxSimilarLogs = 2
	defaultQueueDepth     = 1024
	defaultMaxValueBytes  = 2048
	defaultMaxAttrs       = 64
	redactedValue         = "[REDACTED]"

	FormatAuto    = "auto"
	FormatConsole = "console"
	FormatJSON    = "json"
	FormatCWF     = "cwf"
)

var defaultLogger atomic.Value

type defaultLoggerHolder struct {
	logger Logger
}

// Logger is the Foundation logging facade used by server-kit and scaffolded apps.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
	DebugContext(ctx context.Context, msg string, args ...any)
	InfoContext(ctx context.Context, msg string, args ...any)
	WarnContext(ctx context.Context, msg string, args ...any)
	ErrorContext(ctx context.Context, msg string, args ...any)
	With(args ...any) Logger
	Sync() error
	Dropped() uint64
}

// Config holds the Foundation logger configuration.
type Config struct {
	Environment string // "production" or "development"
	LogLevel    string // "debug", "info", "warn", "error"
	Format      string // "auto", "console", "json", or "cwf"
	ServiceName string
	Component   string
	CallerSkip  int
	Output      io.Writer
	DisableANSI bool

	// Performance controls.
	EnableFiltering bool
	FilterInterval  int
	MaxSimilarLogs  int
	Async           bool
	QueueDepth      int
	MaxAttrs        int
	MaxValueBytes   int
}

type logger struct {
	inner  *slog.Logger
	config Config
	async  *asyncState
}

type slogProvider interface {
	slogLogger() *slog.Logger
}

type handlerConfig struct {
	environment     string
	serviceName     string
	component       string
	enableFiltering bool
	filterInterval  time.Duration
	maxSimilarLogs  int
	maxAttrs        int
	maxValueBytes   int
}

type filterState struct {
	mu    sync.Mutex
	cache map[string]*logFilter
}

type logFilter struct {
	lastLogTime time.Time
	count       int
}

// DefaultConfig returns a development logger with readable, colorized output.
func DefaultConfig() Config {
	return Config{
		Environment:     "development",
		LogLevel:        "info",
		Format:          FormatAuto,
		ServiceName:     "ovasabi-service",
		Component:       "foundation",
		CallerSkip:      3,
		EnableFiltering: true,
		FilterInterval:  int(defaultFilterInterval / time.Millisecond),
		MaxSimilarLogs:  defaultMaxSimilarLogs,
		QueueDepth:      defaultQueueDepth,
		MaxAttrs:        defaultMaxAttrs,
		MaxValueBytes:   defaultMaxValueBytes,
	}
}

// ProductionConfig returns a bounded, async logger for high-throughput services.
func ProductionConfig() Config {
	cfg := DefaultConfig()
	cfg.Environment = "production"
	cfg.LogLevel = "info"
	cfg.Async = true
	cfg.FilterInterval = 1000
	cfg.MaxSimilarLogs = 1
	return cfg
}

// New creates a Foundation logger.
func New(cfg Config) (Logger, error) {
	cfg = normalizeConfig(cfg)
	level := parseLogLevel(cfg.LogLevel)
	hcfg := handlerConfig{
		environment:     strings.ToLower(cfg.Environment),
		serviceName:     cfg.ServiceName,
		component:       cfg.Component,
		enableFiltering: cfg.EnableFiltering,
		filterInterval:  time.Duration(cfg.FilterInterval) * time.Millisecond,
		maxSimilarLogs:  cfg.MaxSimilarLogs,
		maxAttrs:        cfg.MaxAttrs,
		maxValueBytes:   cfg.MaxValueBytes,
	}

	var sink slog.Handler
	switch normalizeFormat(cfg.Format, cfg.Environment) {
	case FormatJSON:
		sink = slog.NewJSONHandler(cfg.Output, &slog.HandlerOptions{
			Level:       level,
			ReplaceAttr: replaceAttr(hcfg),
		})
	case FormatCWF:
		sink = newCWFHandler(cfg.Output, level, hcfg)
	default:
		sink = newConsoleHandler(cfg.Output, level, hcfg, !cfg.DisableANSI)
	}

	var async *asyncState
	if cfg.Async {
		sink, async = newAsyncHandler(sink, cfg.QueueDepth)
	}
	sink = newFoundationHandler(sink, hcfg)
	base := slog.New(sink).With("service", cfg.ServiceName, "component", cfg.Component)
	return &logger{inner: base, config: cfg, async: async}, nil
}

// NewDefault creates a logger with development defaults.
func NewDefault() (Logger, error) {
	return New(DefaultConfig())
}

// NewProduction creates a production logger for a service.
func NewProduction(serviceName string) (Logger, error) {
	cfg := ProductionConfig()
	cfg.ServiceName = serviceName
	return New(cfg)
}

// RuntimeConfig returns a logger config for an application runtime scope.
// Foundation owns the logger behavior; applications only declare identity.
func RuntimeConfig(env, level, serviceName, component string) Config {
	cfg := DefaultConfig()
	if isProduction(env) {
		cfg = ProductionConfig()
	}
	cfg.Environment = env
	cfg.LogLevel = level
	cfg.ServiceName = serviceName
	cfg.Component = component
	return cfg
}

// Install creates and installs the process logger from declarative config.
func Install(cfg Config) Logger {
	l, _ := New(cfg)
	SetDefault(l)
	return l
}

// SetDefault installs a Foundation logger as the process default.
func SetDefault(l Logger) {
	if l == nil {
		return
	}
	defaultLogger.Store(defaultLoggerHolder{logger: l})
	if provider, ok := l.(slogProvider); ok {
		slog.SetDefault(provider.slogLogger())
	}
}

// Default returns the installed logger or a discard-backed development logger.
func Default() Logger {
	if value := defaultLogger.Load(); value != nil {
		if holder, ok := value.(defaultLoggerHolder); ok && holder.logger != nil {
			return holder.logger
		}
	}
	l, _ := New(Config{Output: io.Discard})
	return l
}

func (l *logger) Debug(msg string, args ...any) {
	l.log(context.Background(), slog.LevelDebug, msg, args...)
}

func (l *logger) Info(msg string, args ...any) {
	l.log(context.Background(), slog.LevelInfo, msg, args...)
}

func (l *logger) Warn(msg string, args ...any) {
	l.log(context.Background(), slog.LevelWarn, msg, args...)
}

func (l *logger) Error(msg string, args ...any) {
	l.log(context.Background(), slog.LevelError, msg, args...)
}

func (l *logger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelDebug, msg, args...)
}

func (l *logger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelInfo, msg, args...)
}

func (l *logger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelWarn, msg, args...)
}

func (l *logger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.log(ctx, slog.LevelError, msg, args...)
}

func (l *logger) With(args ...any) Logger {
	return &logger{
		inner:  l.inner.With(args...),
		config: l.config,
		async:  l.async,
	}
}

func (l *logger) Sync() error {
	if l.async != nil {
		l.async.flush(500 * time.Millisecond)
	}
	if syncer, ok := l.config.Output.(interface{ Sync() error }); ok {
		return syncer.Sync()
	}
	return nil
}

func (l *logger) Dropped() uint64 {
	if l.async == nil {
		return 0
	}
	return l.async.dropped.Load()
}

func (l *logger) slogLogger() *slog.Logger {
	return l.inner
}

func (l *logger) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !l.inner.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(l.config.CallerSkip, pcs[:])
	record := slog.NewRecord(time.Now(), level, msg, pcs[0])
	record.Add(args...)
	_ = l.inner.Handler().Handle(ctx, record)
}

func normalizeConfig(cfg Config) Config {
	defaults := DefaultConfig()
	if strings.TrimSpace(cfg.Environment) == "" {
		cfg.Environment = defaults.Environment
	}
	if strings.TrimSpace(cfg.LogLevel) == "" {
		cfg.LogLevel = defaults.LogLevel
	}
	if strings.TrimSpace(cfg.Format) == "" {
		cfg.Format = defaults.Format
	}
	if strings.TrimSpace(cfg.ServiceName) == "" {
		cfg.ServiceName = defaults.ServiceName
	}
	if strings.TrimSpace(cfg.Component) == "" {
		cfg.Component = defaults.Component
	}
	if cfg.CallerSkip <= 0 {
		cfg.CallerSkip = defaults.CallerSkip
	}
	if cfg.Output == nil {
		cfg.Output = os.Stdout
	}
	if cfg.FilterInterval <= 0 {
		cfg.FilterInterval = defaults.FilterInterval
	}
	if cfg.MaxSimilarLogs <= 0 {
		cfg.MaxSimilarLogs = defaults.MaxSimilarLogs
	}
	if cfg.QueueDepth <= 0 {
		cfg.QueueDepth = defaults.QueueDepth
	}
	if cfg.MaxAttrs <= 0 {
		cfg.MaxAttrs = defaults.MaxAttrs
	}
	if cfg.MaxValueBytes <= 0 {
		cfg.MaxValueBytes = defaults.MaxValueBytes
	}
	return cfg
}

func parseLogLevel(levelStr string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(levelStr)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func isProduction(env string) bool {
	return strings.EqualFold(strings.TrimSpace(env), "production")
}

func normalizeFormat(format string, env string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case FormatJSON:
		return FormatJSON
	case FormatCWF:
		return FormatCWF
	case FormatConsole:
		return FormatConsole
	default:
		if isProduction(env) {
			return FormatJSON
		}
		return FormatConsole
	}
}

func GetCaller(depth int) string {
	_, file, line, ok := runtime.Caller(depth)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s:%d", file, line)
}

func contextAttrs(ctx context.Context) []slog.Attr {
	md, ok := metautil.FromContextOK(ctx)
	if !ok {
		return nil
	}
	attrs := make([]slog.Attr, 0, 8)
	addStringAttr(&attrs, "correlation_id", md.CorrelationID)
	addStringAttr(&attrs, "request_id", md.RequestID)
	addStringAttr(&attrs, "trace_id", md.TraceID)
	if md.GlobalContext != nil {
		addStringAttr(&attrs, "organization_id", md.GlobalContext.OrganizationID)
		addStringAttr(&attrs, "actor_id", md.GlobalContext.UserID)
	}
	appendMetadataValue(&attrs, md.Attributes, md.Extras, "event_type")
	appendMetadataValue(&attrs, md.Attributes, md.Extras, "component")
	appendMetadataValue(&attrs, md.Attributes, md.Extras, "projection")
	appendMetadataValue(&attrs, md.Attributes, md.Extras, "epoch")
	return attrs
}

func appendMetadataValue(attrs *[]slog.Attr, stringValues map[string]string, anyValues map[string]any, key string) {
	if stringValues != nil {
		if value := strings.TrimSpace(stringValues[key]); value != "" {
			*attrs = append(*attrs, slog.String(key, value))
			return
		}
	}
	if anyValues != nil {
		if value, ok := anyValues[key]; ok {
			*attrs = append(*attrs, slog.Any(key, value))
		}
	}
}

func addStringAttr(attrs *[]slog.Attr, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	*attrs = append(*attrs, slog.String(key, value))
}
