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

func BenchmarkEqual16_Exact(b *testing.B) {
	s := "POST"
	padded := MakePadded16("POST")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
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
	for i := 0; i < b.N; i++ {
		if !strings.EqualFold(s, target) {
			b.Fatal("mismatch")
		}
	}
}
