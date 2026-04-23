package logger

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is the interface for standardized logging across Ovasabi services.
type Logger interface {
	Info(msg string, fields ...zapcore.Field)
	Error(msg string, fields ...zapcore.Field)
	Debug(msg string, fields ...zapcore.Field)
	Warn(msg string, fields ...zapcore.Field)
	Sync() error
	With(fields ...zapcore.Field) Logger
	GetZapLogger() *zap.Logger
}

// Config holds the configuration for the hardened logger.
type Config struct {
	Environment string // "production" or "development"
	LogLevel    string // "debug", "info", "warn", "error"
	ServiceName string
	CallerSkip  int // Number of stack frames to skip for caller info (default 1)
	
	// Performance filtering (Deduplication)
	EnableFiltering bool // Enable log filtering in production
	FilterInterval  int  // Minimum interval between similar logs (ms)
	MaxSimilarLogs  int  // Maximum similar logs per interval
}

type logger struct {
	zapLogger    *zap.Logger
	config       Config
	filterCache  map[string]*logFilter
	filterMutex  sync.RWMutex
	isProduction bool
}

type logFilter struct {
	lastLogTime time.Time
	count       int
	lastMessage string
}

// DefaultConfig returns a default development configuration.
func DefaultConfig() Config {
	return Config{
		Environment:     "development",
		LogLevel:        "info",
		ServiceName:     "ovasabi-service",
		EnableFiltering: true,
		FilterInterval:  2000,
		MaxSimilarLogs:  2,
		CallerSkip:      1,
	}
}

// ProductionConfig returns a configuration optimized for high-throughput production.
func ProductionConfig() Config {
	return Config{
		Environment:     "production",
		LogLevel:        "warn",
		ServiceName:     "ovasabi-service",
		EnableFiltering: true,
		FilterInterval:  1000,
		MaxSimilarLogs:  1,
		CallerSkip:      1,
	}
}

// New creates a new hardened zap logger.
func New(cfg Config) (Logger, error) {
	level := parseLogLevel(cfg.LogLevel)

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var inner *zap.Logger
	var err error

	if strings.EqualFold(cfg.Environment, "production") {
		zapCfg := zap.NewProductionConfig()
		zapCfg.Level = zap.NewAtomicLevelAt(level)
		inner, err = zapCfg.Build()
	} else {
		zapCfg := zap.NewDevelopmentConfig()
		if level != zap.InfoLevel {
			zapCfg.Level = zap.NewAtomicLevelAt(level)
		}
		inner, err = zapCfg.Build()
	}

	if err != nil {
		return nil, err
	}

	zapLogger := inner

	if cfg.ServiceName != "" {
		zapLogger = zapLogger.With(zap.String("service", cfg.ServiceName))
	}

	return &logger{
		zapLogger:    zapLogger,
		config:       cfg,
		filterCache:  make(map[string]*logFilter),
		isProduction: strings.EqualFold(cfg.Environment, "production"),
	}, nil
}

// NewDefault creates a logger with default development settings.
func NewDefault() (Logger, error) {
	return New(DefaultConfig())
}

// NewProduction creates a logger with production-optimized settings.
func NewProduction(serviceName string) (Logger, error) {
	cfg := ProductionConfig()
	cfg.ServiceName = serviceName
	return New(cfg)
}

func (l *logger) Info(msg string, fields ...zapcore.Field) {
	if l.shouldLog(msg, "info") {
		l.zapLogger.Info(msg, fields...)
	}
}

func (l *logger) Error(msg string, fields ...zapcore.Field) {
	// Errors bypass filtering for maximum visibility
	l.zapLogger.Error(msg, fields...)
}

func (l *logger) Debug(msg string, fields ...zapcore.Field) {
	if l.shouldLog(msg, "debug") {
		l.zapLogger.Debug(msg, fields...)
	}
}

func (l *logger) Warn(msg string, fields ...zapcore.Field) {
	if l.shouldLog(msg, "warn") {
		l.zapLogger.Warn(msg, fields...)
	}
}

func (l *logger) Sync() error {
	return l.zapLogger.Sync()
}

func (l *logger) With(fields ...zapcore.Field) Logger {
	return &logger{
		zapLogger:    l.zapLogger.With(fields...),
		config:       l.config,
		filterCache:  l.filterCache,
		isProduction: l.isProduction,
	}
}

func (l *logger) GetZapLogger() *zap.Logger {
	return l.zapLogger
}

func parseLogLevel(levelStr string) zapcore.Level {
	switch strings.ToLower(levelStr) {
	case "debug": return zapcore.DebugLevel
	case "info":  return zapcore.InfoLevel
	case "warn":  return zapcore.WarnLevel
	case "error": return zapcore.ErrorLevel
	default:      return zapcore.InfoLevel
	}
}

func (l *logger) shouldLog(msg, level string) bool {
	if !l.isProduction || !l.config.EnableFiltering {
		return true
	}

	key := level + ":" + msg
	if len(key) > 64 {
		key = key[:64]
	}

	now := time.Now()
	l.filterMutex.Lock()
	defer l.filterMutex.Unlock()

	filter, exists := l.filterCache[key]
	if !exists {
		l.filterCache[key] = &logFilter{lastLogTime: now, count: 1, lastMessage: msg}
		return true
	}

	if now.Sub(filter.lastLogTime) >= time.Duration(l.config.FilterInterval)*time.Millisecond {
		filter.count = 1
		filter.lastLogTime = now
		return true
	}

	if filter.count >= l.config.MaxSimilarLogs {
		return false
	}

	filter.count++
	return true
}

func GetCaller(depth int) string {
	_, file, line, ok := runtime.Caller(depth)
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s:%d", file, line)
}
