package logger

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type foundationHandler struct {
	next    slog.Handler
	config  handlerConfig
	filters *filterState
}

func newFoundationHandler(next slog.Handler, cfg handlerConfig) slog.Handler {
	return &foundationHandler{
		next:    next,
		config:  cfg,
		filters: &filterState{cache: make(map[string]*logFilter)},
	}
}

func (h *foundationHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *foundationHandler) Handle(ctx context.Context, r slog.Record) error {
	if !h.shouldLog(r.Level, r.Message, r) {
		return nil
	}
	r = r.Clone()
	h.addContext(ctx, &r)
	return h.next.Handle(ctx, r)
}

func (h *foundationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &foundationHandler{next: h.next.WithAttrs(attrs), config: h.config, filters: h.filters}
}

func (h *foundationHandler) WithGroup(name string) slog.Handler {
	return &foundationHandler{next: h.next.WithGroup(name), config: h.config, filters: h.filters}
}

func (h *foundationHandler) shouldLog(level slog.Level, msg string, r slog.Record) bool {
	if level >= slog.LevelError || !h.config.enableFiltering || h.config.environment != "production" {
		return true
	}
	key := filterKey(level, msg, r)
	now := time.Now()
	h.filters.mu.Lock()
	defer h.filters.mu.Unlock()
	filter, exists := h.filters.cache[key]
	if !exists {
		h.filters.cache[key] = &logFilter{lastLogTime: now, count: 1}
		return true
	}
	if now.Sub(filter.lastLogTime) >= h.config.filterInterval {
		filter.count = 1
		filter.lastLogTime = now
		return true
	}
	if filter.count >= h.config.maxSimilarLogs {
		return false
	}
	filter.count++
	return true
}

func (h *foundationHandler) addContext(ctx context.Context, r *slog.Record) {
	attrs := contextAttrs(ctx)
	if len(attrs) == 0 {
		return
	}
	seen := recordKeys(*r)
	for _, attr := range attrs {
		if _, ok := seen[attr.Key]; ok {
			continue
		}
		r.AddAttrs(attr)
	}
}

// filterKey builds a dedup key that includes a discriminator drawn from the
// record's attrs so distinct fanout events (e.g. "registered handler" for many
// event_types, "registered route" for many paths) don't collide under a single
// per-message counter. Without this, a MaxSimilarLogs=1 production config
// silently drops all but the first hit per unique message per interval, which
// hides startup enumeration logs.
func filterKey(level slog.Level, msg string, r slog.Record) string {
	disc := discriminator(r)
	key := level.String() + ":" + msg
	if disc != "" {
		key += "|" + disc
	}
	if len(key) > 160 {
		return key[:160]
	}
	return key
}

// discriminatorKeys are attr keys that identify distinct instances of an
// otherwise-identical log message. Values are concatenated into the filter key.
var discriminatorKeys = []string{"event_type", "path", "route", "name", "handler", "id"}

func discriminator(r slog.Record) string {
	if r.NumAttrs() == 0 {
		return ""
	}
	found := make(map[string]string, len(discriminatorKeys))
	r.Attrs(func(attr slog.Attr) bool {
		for _, k := range discriminatorKeys {
			if attr.Key == k {
				found[k] = attr.Value.Resolve().String()
				break
			}
		}
		return true
	})
	if len(found) == 0 {
		return ""
	}
	var b strings.Builder
	for _, k := range discriminatorKeys {
		if v, ok := found[k]; ok {
			if b.Len() > 0 {
				b.WriteByte(',')
			}
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
		}
	}
	return b.String()
}

func recordKeys(r slog.Record) map[string]struct{} {
	seen := make(map[string]struct{}, r.NumAttrs())
	r.Attrs(func(attr slog.Attr) bool {
		seen[attr.Key] = struct{}{}
		return true
	})
	return seen
}

func replaceAttr(cfg handlerConfig) func([]string, slog.Attr) slog.Attr {
	return func(groups []string, attr slog.Attr) slog.Attr {
		return sanitizeAttr(groups, attr, cfg)
	}
}

func sanitizeAttr(_ []string, attr slog.Attr, cfg handlerConfig) slog.Attr {
	if attr.Key == slog.TimeKey {
		attr.Key = "timestamp"
	}
	if attr.Key == slog.MessageKey {
		attr.Key = "message"
	}
	if attr.Key == slog.LevelKey {
		attr.Key = "level"
		attr.Value = slog.StringValue(strings.ToLower(attr.Value.String()))
	}
	if attr.Key == slog.SourceKey {
		attr.Key = "source"
	}
	if isSensitiveKey(attr.Key) {
		return slog.String(attr.Key, redactedValue)
	}
	return boundAttr(attr, cfg.maxValueBytes)
}

func boundAttr(attr slog.Attr, maxBytes int) slog.Attr {
	attr.Value = attr.Value.Resolve()
	switch attr.Value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(sanitizeString(attr.Value.String(), maxBytes))
	case slog.KindAny:
		if err, ok := attr.Value.Any().(error); ok {
			attr.Value = slog.StringValue(sanitizeString(err.Error(), maxBytes))
		}
	}
	return attr
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

var sensitiveKeyFragments = []string{
	"authorization",
	"bearer",
	"cookie",
	"credential",
	"csrf",
	"jwt",
	"password",
	"private_key",
	"refresh_token",
	"secret",
	"session",
	"token",
	"api_key",
}

func sanitizeString(value string, maxBytes int) string {
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	for _, marker := range sensitiveValueMarkers {
		if strings.Contains(lower, marker) {
			return redactedValue
		}
	}
	if maxBytes <= 0 || len(trimmed) <= maxBytes {
		return trimmed
	}
	return trimmed[:maxBytes] + "..."
}

var sensitiveValueMarkers = []string{
	"authorization:",
	"bearer ",
	"password=",
	"password:",
	"private_key=",
	"secret=",
	"set-cookie:",
	"token=",
}

func formatLevel(level slog.Level, color bool) string {
	label := "INF"
	code := ansiBlue
	switch {
	case level <= slog.LevelDebug:
		label = "DBG"
		code = ansiGray
	case level >= slog.LevelError:
		label = "ERR"
		code = ansiRed
	case level >= slog.LevelWarn:
		label = "WRN"
		code = ansiYellow
	}
	if !color {
		return label
	}
	return code + label + ansiReset
}

func sourcePath(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	frames := runtime.CallersFrames([]uintptr{pc})
	frame, _ := frames.Next()
	if frame.File == "" {
		return ""
	}
	return filepath.Base(filepath.Dir(frame.File)) + "/" + filepath.Base(frame.File) + ":" + strconv.Itoa(frame.Line)
}

func appendAttr(parts *[]string, attr slog.Attr, cfg handlerConfig, color bool) {
	attr = sanitizeAttr(nil, attr, cfg)
	if attr.Key == "" || attr.Key == "service" || attr.Key == "component" {
		return
	}
	*parts = append(*parts, fieldKey(attr.Key, color)+"="+formatValue(attr.Value))
}

func formatValue(value slog.Value) string {
	value = value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		return quoteIfNeeded(value.String())
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().Format(time.RFC3339Nano)
	default:
		return quoteIfNeeded(fmt.Sprint(value.Any()))
	}
}

func quoteIfNeeded(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\r|") {
		return strconv.Quote(value)
	}
	return value
}

func fieldKey(key string, color bool) string {
	if !color {
		return key
	}
	return ansiGray + key + ansiReset
}

const (
	ansiReset  = "\033[0m"
	ansiGray   = "\033[90m"
	ansiCyan   = "\033[36m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiBlue   = "\033[34m"
)
