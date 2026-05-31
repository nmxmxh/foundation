package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cwfHandler writes Compact Wire Format log lines:
// cwf.v1<TAB>unix_nano<TAB>level<TAB>service<TAB>component<TAB>message<TAB>k=v...
// It keeps production log storage cheap to append, split, and index while still
// passing through Foundation redaction, bounds, filtering, and context fields.
type cwfHandler struct {
	out    io.Writer
	level  slog.Leveler
	config handlerConfig
	mu     *sync.Mutex
	attrs  []slog.Attr
	groups []string
}

func newCWFHandler(out io.Writer, level slog.Level, cfg handlerConfig) slog.Handler {
	return &cwfHandler{
		out:    out,
		level:  level,
		config: cfg,
		mu:     &sync.Mutex{},
	}
}

func (h *cwfHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *cwfHandler) Handle(_ context.Context, r slog.Record) error {
	service, component, attrs := h.collectAttrs(r)
	line := h.renderLine(r, service, component, attrs)
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.out.Write([]byte(line))
	return err
}

func (h *cwfHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := h.clone()
	next.attrs = append(next.attrs, attrs...)
	return next
}

func (h *cwfHandler) WithGroup(name string) slog.Handler {
	next := h.clone()
	next.groups = append(next.groups, name)
	return next
}

func (h *cwfHandler) clone() *cwfHandler {
	attrs := make([]slog.Attr, len(h.attrs))
	copy(attrs, h.attrs)
	groups := make([]string, len(h.groups))
	copy(groups, h.groups)
	return &cwfHandler{
		out:    h.out,
		level:  h.level,
		config: h.config,
		mu:     h.mu,
		attrs:  attrs,
		groups: groups,
	}
}

func (h *cwfHandler) collectAttrs(r slog.Record) (string, string, []slog.Attr) {
	all := make([]slog.Attr, 0, len(h.attrs)+r.NumAttrs())
	all = append(all, h.attrs...)
	r.Attrs(func(attr slog.Attr) bool {
		all = append(all, attr)
		return len(all) < h.config.maxAttrs
	})
	service := h.config.serviceName
	component := h.config.component
	for _, attr := range all {
		attr = sanitizeAttr(h.groups, attr, h.config)
		switch attr.Key {
		case "service":
			service = attr.Value.String()
		case "component":
			component = attr.Value.String()
		}
	}
	return service, component, all
}

func (h *cwfHandler) renderLine(r slog.Record, service, component string, attrs []slog.Attr) string {
	var b strings.Builder
	b.Grow(224)
	b.WriteString("cwf.v1")
	b.WriteByte('\t')
	b.WriteString(strconv.FormatInt(r.Time.UnixNano(), 10))
	b.WriteByte('\t')
	b.WriteString(levelValue(r.Level))
	b.WriteByte('\t')
	b.WriteString(escapeCWF(service))
	b.WriteByte('\t')
	b.WriteString(escapeCWF(component))
	b.WriteByte('\t')
	b.WriteString(escapeCWF(sanitizeString(r.Message, h.config.maxValueBytes)))

	for _, attr := range attrs {
		attr = sanitizeAttr(nil, attr, h.config)
		if attr.Key == "" || attr.Key == "service" || attr.Key == "component" {
			continue
		}
		appendCWFField(&b, attr.Key, cwfValue(attr.Value))
	}
	if src := sourcePath(r.PC); src != "" {
		appendCWFField(&b, "source", src)
	}
	b.WriteByte('\n')
	return b.String()
}

func appendCWFField(b *strings.Builder, key string, value string) {
	b.WriteByte('\t')
	b.WriteString(escapeCWFKey(key))
	b.WriteByte('=')
	b.WriteString(escapeCWF(value))
}

func cwfValue(value slog.Value) string {
	value = value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindTime:
		return value.Time().Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(value.Any())
	}
}

func levelValue(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return "debug"
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warn"
	default:
		return "info"
	}
}

func escapeCWFKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "_"
	}
	return escapeCWF(value)
}

func escapeCWF(value string) string {
	return cwfEscaper.Replace(value)
}

var cwfEscaper = strings.NewReplacer(
	`\`, `\\`,
	"\t", `\t`,
	"\n", `\n`,
	"\r", `\r`,
	"=", `\=`,
)
