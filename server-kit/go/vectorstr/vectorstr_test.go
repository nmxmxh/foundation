package vectorstr

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"
)

func TestEqual16(t *testing.T) {
	tests := []struct {
		s      string
		target string
		want   bool
	}{
		{"", "", true},
		{"a", "a", true},
		{"a", "b", false},
		{"GET", "GET", true},
		{"GET", "POST", false},
		{"POST", "POST", true},
		{"OPTIONS", "OPTIONS", true},
		{"OPTIONS", "OPTION", false},
		{"12345678", "12345678", true},
		{"12345678", "12345679", false},
		{"123456789", "123456789", true},
		{"123456789", "123456780", false},
		{"1234567890123456", "1234567890123456", true},
		{"1234567890123456", "1234567890123457", false},
		{"very_long_string_exceeding_16", "very_long_string_exceeding_16", false}, // exceeds 16
		{"1234567812345678extra", "1234567812345678", false},
		{"1234567890123456", "1234567890123456", true},
		{"abcdefgh12345678", "abcdefgh12345679", false},
		{"diff123456789012", "orig123456789012", false},
		{"GET", "GETTING", false},
		{"short", "longer_str", false},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s_vs_%s", tc.s, tc.target), func(t *testing.T) {
			padded := MakePadded16(tc.target)
			got := Equal16(tc.s, &padded, len(tc.target))
			if got != tc.want {
				t.Errorf("Equal16(%q, %q) = %v; want %v", tc.s, tc.target, got, tc.want)
			}
		})
	}
}

func TestEqualFold16(t *testing.T) {
	tests := []struct {
		s      string
		target string
		want   bool
	}{
		{"", "", true},
		{"get", "get", true},
		{"GET", "get", true},
		{"Get", "get", true},
		{"post", "post", true},
		{"POST", "post", true},
		{"Content-Type", "content-type", true},
		{"CONTENT-TYPE", "content-type", true},
		{"Authorization", "authorization", true},
		{"AUTHORIZATION", "authorization", true},
		{"wrong", "right", false},
		{"short", "longer_target", false},
		{"long_string_over_16_chars", "long_string_over_16_chars", false},
		{"12345678abcdef", "12345678uvwxyz", false},
		{"diff123456789012", "orig123456789012", false},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s_vs_%s", tc.s, tc.target), func(t *testing.T) {
			lowerTarget := strings.ToLower(tc.target)
			padded := MakePadded16(lowerTarget)
			got := EqualFold16(tc.s, &padded, len(tc.target))
			if got != tc.want {
				t.Errorf("EqualFold16(%q, %q) = %v; want %v", tc.s, tc.target, got, tc.want)
			}
		})
	}
}

func TestPageBoundaryFallback(t *testing.T) {
	// Allocate a slice and position a string at the very edge of an aligned page
	const pageSize = 4096
	buf := make([]byte, pageSize*2)
	addr := uintptr(unsafe.Pointer(&buf[0]))
	offsetToNextPage := pageSize - int(addr%pageSize)

	// Place string 4 bytes before page end (violates PageBoundaryRoom)
	start := offsetToNextPage - 4
	copy(buf[start:start+4], "TEST")
	edgeStr := unsafe.String(&buf[start], 4)

	padded := MakePadded16("TEST")
	if !Equal16(edgeStr, &padded, 4) {
		t.Errorf("Equal16 near page boundary failed")
	}

	lowerPadded := MakePadded16("test")
	if !EqualFold16(edgeStr, &lowerPadded, 4) {
		t.Errorf("EqualFold16 near page boundary failed")
	}

	lowerMismatch := MakePadded16("diff")
	if EqualFold16(edgeStr, &lowerMismatch, 4) {
		t.Errorf("EqualFold16 near page boundary should have failed on mismatch")
	}
}

// refEqualFold is the reference: ASCII-only case fold, exact match otherwise.
func refEqualFold(s, lowerTarget string) bool {
	if len(s) != len(lowerTarget) || len(s) > MaxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 'A' && b <= 'Z' {
			b |= 0x20
		}
		if b != lowerTarget[i] {
			return false
		}
	}
	return true
}

// TestEqualFold16RejectsNonLetterFold guards against folding bit 5 on non-letters.
func TestEqualFold16RejectsNonLetterFold(t *testing.T) {
	cases := [][2]string{
		{"content\rtype", "content-type"},
		{"@", "`"},
		{"[", "{"},
		{"^", "~"},
		{"\x00", " "},
		{"_", "\x7f"},
	}
	for _, c := range cases {
		padded := MakePadded16(c[1])
		if EqualFold16(c[0], &padded, len(c[1])) {
			t.Errorf("EqualFold16(%q, %q) = true; want false", c[0], c[1])
		}
	}
}

// TestEqualFold16ExhaustiveBytePairs checks every byte pair at every position.
func TestEqualFold16ExhaustiveBytePairs(t *testing.T) {
	base := []byte("abcdefghijklmnop")
	for pos := range MaxLen {
		for a := range 256 {
			for b := range 256 {
				in := append([]byte(nil), base...)
				tg := append([]byte(nil), base...)
				in[pos], tg[pos] = byte(a), byte(b)
				padded := MakePadded16(string(tg))
				got := EqualFold16(string(in), &padded, len(tg))
				if want := refEqualFold(string(in), string(tg)); got != want {
					t.Fatalf("pos %d: EqualFold16(%#x vs %#x) = %v; want %v", pos, a, b, got, want)
				}
			}
		}
	}
}

// TestEqual16AllLengths checks every length from 0 to 17 and a one-byte change.
func TestEqual16AllLengths(t *testing.T) {
	src := "0123456789abcdefg"
	for n := 0; n <= MaxLen+1; n++ {
		s := src[:n]
		padded := MakePadded16(s)
		if got, want := Equal16(s, &padded, n), n <= MaxLen; got != want {
			t.Fatalf("len %d: Equal16 = %v; want %v", n, got, want)
		}
		if n > 0 && n <= MaxLen {
			mut := []byte(s)
			mut[n-1] ^= 0x01
			if Equal16(string(mut), &padded, n) {
				t.Fatalf("len %d: Equal16 matched a changed last byte", n)
			}
		}
	}
}

func FuzzEqualFold16(f *testing.F) {
	f.Add("Content-Type", "content-type")
	f.Add("content\rtype", "content-type")
	f.Add("\xc3\x89", "\xc3\xa9")
	f.Fuzz(func(t *testing.T, s, target string) {
		if len(target) > MaxLen {
			return
		}
		padded := MakePadded16(target)
		if got, want := EqualFold16(s, &padded, len(target)), refEqualFold(s, target); got != want {
			t.Fatalf("EqualFold16(%q, %q) = %v; want %v", s, target, got, want)
		}
		if got, want := Equal16(s, &padded, len(target)), s == target; got != want {
			t.Fatalf("Equal16(%q, %q) = %v; want %v", s, target, got, want)
		}
	})
}

func BenchmarkEqual16_Exact(b *testing.B) {
	s := "POST"
	padded := MakePadded16("POST")
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if !Equal16(s, &padded, 4) {
			b.Fatal("mismatch")
		}
	}
}

func BenchmarkGoStandardStringEqual(b *testing.B) {
	s := "POST"
	target := "POST"
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if s != target {
			b.Fatal("mismatch")
		}
	}
}

func BenchmarkEqualFold16(b *testing.B) {
	s := "Content-Type"
	padded := MakePadded16("content-type")
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if !EqualFold16(s, &padded, 12) {
			b.Fatal("mismatch")
		}
	}
}

func BenchmarkStringsEqualFold(b *testing.B) {
	s := "Content-Type"
	target := "content-type"
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if !strings.EqualFold(s, target) {
			b.Fatal("mismatch")
		}
	}
}

func TestLookupCanonicalHeader(t *testing.T) {
	cases := []struct {
		header string
		wantID CanonicalHeaderID
		wantOk bool
	}{
		{"authorization", HeaderAuthorization, true},
		{"content-type", HeaderContentType, true},
		{"x-correlation-id", HeaderCorrelationID, true},
		{"x-request-id", HeaderRequestID, true},
		{"x-tenant-id", HeaderTenantID, true},
		{"x-device-id", HeaderDeviceID, true},
		{"accept", HeaderAccept, true},
		{"", HeaderUnknown, false},
		{"a", HeaderUnknown, false},
		{"ac", HeaderUnknown, false},
		{"unknown-header", HeaderUnknown, false},
		{"x-custom-id", HeaderUnknown, false},
	}

	for _, c := range cases {
		id, ok := LookupCanonicalHeader(c.header)
		if ok != c.wantOk || id != c.wantID {
			t.Errorf("LookupCanonicalHeader(%q) = (%v, %v); want (%v, %v)", c.header, id, ok, c.wantID, c.wantOk)
		}
	}
}

func BenchmarkLookupCanonicalHeader_PerfectHash(b *testing.B) {
	headers := []string{
		"authorization",
		"content-type",
		"x-correlation-id",
		"x-request-id",
		"x-tenant-id",
		"x-device-id",
		"accept",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		h := headers[i%len(headers)]
		id, ok := LookupCanonicalHeader(h)
		_ = id
		_ = ok
	}
}

func BenchmarkLookupCanonicalHeader_MapBaseline(b *testing.B) {
	headerMap := map[string]CanonicalHeaderID{
		"authorization":    HeaderAuthorization,
		"content-type":     HeaderContentType,
		"x-correlation-id": HeaderCorrelationID,
		"x-request-id":     HeaderRequestID,
		"x-tenant-id":      HeaderTenantID,
		"x-device-id":      HeaderDeviceID,
		"accept":           HeaderAccept,
	}

	headers := []string{
		"authorization",
		"content-type",
		"x-correlation-id",
		"x-request-id",
		"x-tenant-id",
		"x-device-id",
		"accept",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		h := headers[i%len(headers)]
		id, ok := headerMap[h]
		_ = id
		_ = ok
	}
}
