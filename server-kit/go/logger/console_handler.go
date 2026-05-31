package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

type consoleHandler struct {
	out    io.Writer
	level  slog.Leveler
	config handlerConfig
	color  bool
	mu     *sync.Mutex
	attrs  []slog.Attr
	groups []string
}

func newConsoleHandler(out io.Writer, level slog.Level, cfg handlerConfig, color bool) slog.Handler {
	return &consoleHandler{
		out:    out,
		level:  level,
		config: cfg,
		color:  color,
		mu:     &sync.Mutex{},
	}
}

func (h *consoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	service, component, attrs := h.collectAttrs(r)
	line := h.renderLine(r, service, component, attrs)
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.out.Write([]byte(line))
	return err
}

func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := h.clone()
	next.attrs = append(next.attrs, attrs...)
	return next
}

func (h *consoleHandler) WithGroup(name string) slog.Handler {
	next := h.clone()
	next.groups = append(next.groups, name)
	return next
}

func (h *consoleHandler) clone() *consoleHandler {
	attrs := make([]slog.Attr, len(h.attrs))
	copy(attrs, h.attrs)
	groups := make([]string, len(h.groups))
	copy(groups, h.groups)
	return &consoleHandler{
		out:    h.out,
		level:  h.level,
		config: h.config,
		color:  h.color,
		mu:     h.mu,
		attrs:  attrs,
		groups: groups,
	}
}

func (h *consoleHandler) collectAttrs(r slog.Record) (string, string, []slog.Attr) {
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

func (h *consoleHandler) renderLine(r slog.Record, service, component string, attrs []slog.Attr) string {
	var b strings.Builder
	b.Grow(192)
	b.WriteString(colorize(r.Time.Format("15:04:05.000"), ansiGray, h.color))
	b.WriteByte(' ')
	b.WriteString(formatLevel(r.Level, h.color))
	b.WriteByte(' ')
	b.WriteString(colorize(fmt.Sprintf("%-24s", trimMiddle("["+service+"/"+component+"]", 24)), ansiCyan, h.color))
	b.WriteByte(' ')
	b.WriteString(trimMiddle(sanitizeString(r.Message, h.config.maxValueBytes), 64))

	parts := make([]string, 0, len(attrs)+1)
	for _, attr := range attrs {
		appendAttr(&parts, attr, h.config, h.color)
	}
	if src := sourcePath(r.PC); src != "" {
		parts = append(parts, fieldKey("source", h.color)+"="+quoteIfNeeded(src))
	}
	if len(parts) > 0 {
		b.WriteByte(' ')
		b.WriteString(strings.Join(parts, " "))
	}
	b.WriteByte('\n')
	return b.String()
}

func colorize(value, code string, enabled bool) string {
	if !enabled {
		return value
	}
	return code + value + ansiReset
}

func trimMiddle(value string, maxLen int) string {
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	if maxLen <= 3 {
		return value[:maxLen]
	}
	head := (maxLen - 3) / 2
	tail := maxLen - 3 - head
	return value[:head] + "..." + value[len(value)-tail:]
}
