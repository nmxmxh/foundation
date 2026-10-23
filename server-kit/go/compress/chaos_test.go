package compress

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// TestPreferredEncodingChaosAndAdversarial tests parser behavior with erratic headers.
func TestPreferredEncodingChaosAndAdversarial(t *testing.T) {
	cases := []struct {
		name     string
		header   string
		expected string
	}{
		{
			name:     "nan_weight_rejected",
			header:   "br;q=NaN, gzip;q=0.8",
			expected: EncodingGzip,
		},
		{
			name:     "positive_inf_weight_clamped_to_one",
			header:   "gzip;q=Inf, br;q=0.9",
			expected: EncodingBrotli, // both valid > 0; server prefers br
		},
		{
			name:     "negative_inf_weight_rejected",
			header:   "br;q=-Inf, gzip;q=0.7",
			expected: EncodingGzip,
		},
		{
			name:     "negative_weight_clamped",
			header:   "br;q=-1.5, gzip;q=0.5",
			expected: EncodingGzip,
		},
		{
			name:     "huge_weight_clamped_to_one",
			header:   "gzip;q=999999999.9, br;q=1.0",
			expected: EncodingBrotli, // both clamped to 1.0; br has precedence
		},
		{
			name:     "zero_weight_suppressed",
			header:   "br;q=0, gzip;q=0.5",
			expected: EncodingGzip,
		},
		{
			name:     "all_zero_weights",
			header:   "br;q=0, gzip;q=0, zstd;q=0, deflate;q=0",
			expected: "",
		},
		{
			name:     "semicolons_only",
			header:   ";;;;;;;;;",
			expected: "",
		},
		{
			name:     "commas_only",
			header:   ",,,,,,,,,",
			expected: "",
		},
		{
			name:     "excessive_commas_and_whitespace",
			header:   "   ,   ;;   ,   gzip   ;   q = 0.5   ,   ;   ",
			expected: EncodingGzip,
		},
		{
			name:     "missing_q_value",
			header:   "br;q=, gzip;q=0.5",
			expected: EncodingBrotli, // q= syntax error leaves default 1.0
		},
		{
			name:     "duplicate_tokens_highest_wins",
			header:   "gzip;q=0.2, gzip;q=0.9, deflate;q=0.5",
			expected: EncodingGzip,
		},
		{
			name:     "unknown_encodings_ignored",
			header:   "compress, lzma, snappy, bzip2, pack200-gzip;q=1.0",
			expected: "",
		},
		{
			name:     "wildcard_with_low_weight",
			header:   "*;q=0.01, br;q=0.5",
			expected: EncodingBrotli,
		},
		{
			name:     "wildcard_with_specific_encoding",
			header:   "*;q=0.9, gzip;q=0.1",
			expected: EncodingGzip, // explicit supported encoding overrides wildcard
		},
		{
			name:     "null_and_binary_bytes",
			header:   "br\x00extra, \xff\xfe\x01, gzip;q=0.9",
			expected: EncodingGzip,
		},
		{
			name:     "extra_parameters_preserved",
			header:   "gzip;q=0.8;extra=param",
			expected: EncodingGzip,
		},
		{
			name:     "case_permutations",
			header:   "GzIp;Q=0.7, BR;q=0.9, ZsTd;Q=0.8",
			expected: EncodingBrotli,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PreferredEncoding(tc.header)
			if got != tc.expected {
				t.Fatalf("header %q: got %q, want %q", tc.header, got, tc.expected)
			}
		})
	}
}

// TestPreferredEncodingFuzzChaos generates random byte sequences to test parser safety.
func TestPreferredEncodingFuzzChaos(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	tokens := []string{"gzip", "br", "zstd", "deflate", "identity", "*", "q=", "0.", "1.", ";", ",", " "}

	validOutputs := map[string]bool{
		EncodingBrotli:  true,
		EncodingZstd:    true,
		EncodingGzip:    true,
		EncodingDeflate: true,
		"":              true,
	}

	for i := 0; i < 2000; i++ {
		var builder strings.Builder
		tokenCount := rng.Intn(15) + 1
		for j := 0; j < tokenCount; j++ {
			if rng.Float64() < 0.8 {
				builder.WriteString(tokens[rng.Intn(len(tokens))])
			} else {
				// Random chaotic bytes.
				randomBytes := make([]byte, rng.Intn(8)+1)
				rng.Read(randomBytes)
				builder.Write(randomBytes)
			}
		}

		rawHeader := builder.String()
		result := PreferredEncoding(rawHeader)
		if !validOutputs[result] {
			t.Fatalf("iteration %d: unexpected output %q for header %q", i, result, rawHeader)
		}
	}
}

// TestDecompressChaosAndCorruption asserts decoders reject corrupted streams cleanly.
func TestDecompressChaosAndCorruption(t *testing.T) {
	plainText := bytes.Repeat([]byte("Production database engine record block payload.\n"), 100)

	codecs := []struct {
		name       string
		compress   func([]byte) ([]byte, error)
		decompress func([]byte, int64) ([]byte, error)
	}{
		{
			name: "brotli",
			compress: func(b []byte) ([]byte, error) {
				return CompressBrotli(b, 4)
			},
			decompress: func(b []byte, max int64) ([]byte, error) {
				return DecompressWithEncodingLimit(b, EncodingBrotli, max)
			},
		},
		{
			name: "gzip",
			compress: func(b []byte) ([]byte, error) {
				return CompressGzip(b, 5)
			},
			decompress: func(b []byte, max int64) ([]byte, error) {
				return DecompressWithEncodingLimit(b, EncodingGzip, max)
			},
		},
		{
			name: "zstd",
			compress: func(b []byte) ([]byte, error) {
				return CompressZstd(b)
			},
			decompress: func(b []byte, max int64) ([]byte, error) {
				return DecompressWithEncodingLimit(b, EncodingZstd, max)
			},
		},
	}

	for _, c := range codecs {
		t.Run(c.name, func(t *testing.T) {
			compressed, err := c.compress(plainText)
			if err != nil {
				t.Fatalf("compression failed: %v", err)
			}

			// Subtest 1: Truncated payloads.
			t.Run("truncation", func(t *testing.T) {
				step := len(compressed) / 5
				if step < 1 {
					step = 1
				}
				for cut := 1; cut < len(compressed); cut += step {
					truncated := compressed[:cut]
					_, err := c.decompress(truncated, int64(len(plainText)*2))
					if err == nil {
						t.Fatalf("expected error on truncated input of length %d", cut)
					}
				}
			})

			// Subtest 2: Single bit flips in header and payload.
			t.Run("bit_flips", func(t *testing.T) {
				for _, offset := range []int{0, 1, 2, len(compressed) / 2, len(compressed) - 1} {
					corrupted := make([]byte, len(compressed))
					copy(corrupted, compressed)
					corrupted[offset] ^= 0xFF // Flip bits

					_, _ = c.decompress(corrupted, int64(len(plainText)*2))
					// The key invariant is that it does not panic, hang, or crash.
				}
			})

			// Subtest 3: Random byte garbage.
			t.Run("random_garbage", func(t *testing.T) {
				garbage := make([]byte, 256)
				for i := range garbage {
					garbage[i] = byte(i * 37)
				}
				_, err := c.decompress(garbage, int64(len(plainText)*2))
				if err == nil {
					t.Fatalf("expected error on random garbage bytes")
				}
			})
		})
	}
}

// TestOversizedHeaderBoundedExecution verifies that large headers run without excessive latency.
func TestOversizedHeaderBoundedExecution(t *testing.T) {
	// Build a 64 KiB header with repetitive tokens and commas.
	var builder strings.Builder
	for builder.Len() < 64*1024 {
		builder.WriteString("gzip;q=0.5, deflate;q=0.4, br;q=0.9, zstd;q=0.8, ")
	}
	header := builder.String()

	start := time.Now()
	res := PreferredEncoding(header)
	elapsed := time.Since(start)

	if res != EncodingBrotli {
		t.Fatalf("got %q, want %q", res, EncodingBrotli)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("parsing 64 KiB header took too long: %v", elapsed)
	}
}
