package database

import (
	"errors"
	"strings"
	"time"
)

// ErrInvalidTimestamp indicates that a raw string could not be parsed into a valid timestamp.
var ErrInvalidTimestamp = errors.New("invalid timestamp format")

// pgTimestampLayouts lists the supported timestamp string layouts for parsing.
var pgTimestampLayouts = [...]string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999-07",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02 15:04:05-07",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
}

// FormatTimeRFC3339UTC formats a time instance into canonical RFC3339 UTC format.
// Zero time values return an empty string.
func FormatTimeRFC3339UTC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// NormalizeTimestampUTC parses a raw timestamp string and returns canonical RFC3339 UTC text.
// It supports PostgreSQL text format and ISO-8601 variations.
func NormalizeTimestampUTC(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}

	// Fast path for canonical UTC RFC3339Nano timestamps.
	if strings.HasSuffix(trimmed, "Z") && strings.ContainsRune(trimmed, 'T') {
		if _, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return trimmed, nil
		}
	}

	for _, layout := range pgTimestampLayouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.UTC().Format(time.RFC3339Nano), nil
		}
	}

	return "", ErrInvalidTimestamp
}

// FormatRFC3339UTC converts raw timestamp text into canonical RFC3339 UTC format.
// It returns the trimmed input if parsing fails.
func FormatRFC3339UTC(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	normalized, err := NormalizeTimestampUTC(trimmed)
	if err != nil {
		return trimmed
	}
	return normalized
}
