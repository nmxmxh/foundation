package database

import (
	"testing"
	"time"
)

func TestFormatTimeRFC3339UTC(t *testing.T) {
	if got := FormatTimeRFC3339UTC(time.Time{}); got != "" {
		t.Fatalf("expected empty string for zero time, got %q", got)
	}

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location failed: %v", err)
	}

	localTime := time.Date(2026, 10, 24, 18, 58, 0, 123456000, loc)
	expected := "2026-10-24T22:58:00.123456Z"
	got := FormatTimeRFC3339UTC(localTime)
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestNormalizeTimestampUTC(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "empty input",
			input:    "",
			expected: "",
			wantErr:  false,
		},
		{
			name:     "whitespace input",
			input:    "   ",
			expected: "",
			wantErr:  false,
		},
		{
			name:     "canonical RFC3339Nano UTC",
			input:    "2026-10-24T22:58:00.123456Z",
			expected: "2026-10-24T22:58:00.123456Z",
			wantErr:  false,
		},
		{
			name:     "canonical RFC3339 second-precision UTC",
			input:    "2026-10-24T22:58:00Z",
			expected: "2026-10-24T22:58:00Z",
			wantErr:  false,
		},
		{
			name:     "PostgreSQL timestamptz with microsecond and offset",
			input:    "2026-10-24 22:58:00.123456+00",
			expected: "2026-10-24T22:58:00.123456Z",
			wantErr:  false,
		},
		{
			name:     "PostgreSQL timestamptz with offset and minutes",
			input:    "2026-10-24 22:58:00.123456+00:00",
			expected: "2026-10-24T22:58:00.123456Z",
			wantErr:  false,
		},
		{
			name:     "PostgreSQL timestamptz without microseconds",
			input:    "2026-10-24 22:58:00+00",
			expected: "2026-10-24T22:58:00Z",
			wantErr:  false,
		},
		{
			name:     "PostgreSQL timestamptz with non-UTC offset",
			input:    "2026-10-24 22:58:00.123456+02:00",
			expected: "2026-10-24T20:58:00.123456Z",
			wantErr:  false,
		},
		{
			name:     "PostgreSQL space-delimited without timezone assumes UTC",
			input:    "2026-10-24 22:58:00.123456",
			expected: "2026-10-24T22:58:00.123456Z",
			wantErr:  false,
		},
		{
			name:     "invalid string",
			input:    "not-a-timestamp",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeTimestampUTC(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NormalizeTimestampUTC(%q) err = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
			if got != tc.expected {
				t.Fatalf("NormalizeTimestampUTC(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestFormatRFC3339UTC(t *testing.T) {
	pgOutput := "2026-10-24 22:58:00.123456+00"
	expected := "2026-10-24T22:58:00.123456Z"
	if got := FormatRFC3339UTC(pgOutput); got != expected {
		t.Fatalf("FormatRFC3339UTC(%q) = %q, want %q", pgOutput, got, expected)
	}

	invalid := "malformed-timestamp"
	if got := FormatRFC3339UTC(invalid); got != invalid {
		t.Fatalf("FormatRFC3339UTC fallback = %q, want %q", got, invalid)
	}
}

func BenchmarkFormatRFC3339UTC_FastPath(b *testing.B) {
	canonical := "2026-10-24T22:58:00.123456Z"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FormatRFC3339UTC(canonical)
	}
}

func BenchmarkFormatRFC3339UTC_NormalizePG(b *testing.B) {
	pgOutput := "2026-10-24 22:58:00.123456+00"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FormatRFC3339UTC(pgOutput)
	}
}
