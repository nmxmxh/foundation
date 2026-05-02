package logger

import (
	"fmt"
	"strings"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

// BeautifulEncoder is a custom zapcore.Encoder that produces table-aligned,
// color-coded, and compact logs inspired by high-density diagnostic displays.
type BeautifulEncoder struct {
	zapcore.Encoder
	pool buffer.Pool
}

func NewBeautifulEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return &BeautifulEncoder{
		Encoder: zapcore.NewJSONEncoder(cfg), // Use JSON as base but we override EncodeEntry
		pool:    buffer.NewPool(),
	}
}

func (e *BeautifulEncoder) EncodeEntry(entry zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	line := e.pool.Get()

	// 1. Timestamp (Compact)
	line.AppendString(colorGray(entry.Time.Format("15:04:05.000")))
	line.AppendString(" ")

	// 2. Level (Fixed width + Color)
	line.AppendString(formatLevel(entry.Level))
	line.AppendString(" ")

	// 3. Component/Service (Fixed width)
	component := "foundation"
	service := "sys"
	for _, f := range fields {
		if f.Key == "component" {
			component = fmt.Sprintf("%v", f.Interface)
		}
		if f.Key == "service" {
			service = fmt.Sprintf("%v", f.Interface)
		}
	}
	
	compStr := fmt.Sprintf("[%s/%s]", service, component)
	if len(compStr) > 20 {
		compStr = compStr[:17] + "..."
	}
	line.AppendString(colorCyan(fmt.Sprintf("%-20s", compStr)))
	line.AppendString(" ")

	// 4. Message (Main content)
	msg := entry.Message
	if len(msg) > 50 {
		msg = msg[:47] + "..."
	}
	line.AppendString(fmt.Sprintf("%-50s", msg))
	line.AppendString(" ")

	// 5. Metadata Density (The "Magic" part)
	// We represent remaining fields as a compact density string or tiny tags
	var meta []string
	for _, f := range fields {
		if f.Key == "component" || f.Key == "service" {
			continue
		}
		// Skip long IDs but show their presence
		val := fmt.Sprintf("%v", f.Interface)
		if len(val) > 8 {
			val = val[:8] + ".."
		}
		meta = append(meta, fmt.Sprintf("%s=%s", colorGray(f.Key), val))
	}

	if len(meta) > 0 {
		line.AppendString(strings.Join(meta, " "))
	}

	// 6. Caller (End of line, subtle)
	if entry.Caller.Defined {
		line.AppendString(" ")
		line.AppendString(colorGray("→ " + entry.Caller.TrimmedPath()))
	}

	line.AppendString("\n")
	return line, nil
}

// Clone implements zapcore.Encoder.
func (e *BeautifulEncoder) Clone() zapcore.Encoder {
	return &BeautifulEncoder{
		Encoder: e.Encoder.Clone(),
		pool:    e.pool,
	}
}

// Color utilities (ANSI)
func colorGray(s string) string { return "\033[90m" + s + "\033[0m" }
func colorCyan(s string) string { return "\033[36m" + s + "\033[0m" }
func colorRed(s string) string  { return "\033[31m" + s + "\033[0m" }
func colorYellow(s string) string { return "\033[33m" + s + "\033[0m" }
func colorBlue(s string) string { return "\033[34m" + s + "\033[0m" }

func formatLevel(l zapcore.Level) string {
	switch l {
	case zapcore.DebugLevel:
		return colorGray("DEB")
	case zapcore.InfoLevel:
		return colorBlue("INF")
	case zapcore.WarnLevel:
		return colorYellow("WRN")
	case zapcore.ErrorLevel:
		return colorRed("ERR")
	default:
		return l.CapitalString()[:3]
	}
}
