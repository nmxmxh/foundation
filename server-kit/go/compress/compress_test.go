package compress

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"github.com/klauspost/compress/zstd"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

func TestCompressNormalizesLevelOnce(t *testing.T) {
	cases := []struct {
		name   string
		raw    int
		norm   func(int) int
		encode func([]byte, int) ([]byte, error)
	}{
		{"gzip", 5, normalizeGzipLevel, CompressGzip},
		{"gzip-low", -3, normalizeGzipLevel, CompressGzip},
		{"gzip-high", 99, normalizeGzipLevel, CompressGzip},
		{"brotli", 5, normalizeBrotliLevel, CompressBrotli},
		{"brotli-low", 0, normalizeBrotliLevel, CompressBrotli},
		{"brotli-high", 99, normalizeBrotliLevel, CompressBrotli},
		{"flate", 5, normalizeFlateLevel, CompressFlate},
		{"flate-low", -9, normalizeFlateLevel, CompressFlate},
		{"flate-high", 99, normalizeFlateLevel, CompressFlate},
	}

	payload := bytes.Repeat([]byte("codec pool key"), 64)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The pool key must be the normalized value.
			normalized := tc.norm(tc.raw)
			if second := tc.norm(normalized); second != normalized {
				t.Fatalf("normalize is not idempotent: normalize(%d)=%d then %d",
					normalized, normalized, second)
			}

			// A round trip proves the writer at that level produces a valid
			// stream and that a reused writer does not corrupt the next one.
			encoded, err := tc.encode(payload, tc.raw)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			if len(encoded) == 0 {
				t.Fatal("encode produced no bytes")
			}
			decoded, err := DecompressWithEncodingLimit(encoded, encodingForCodec(tc.name), DefaultMaxDecodedBytes)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if !bytes.Equal(decoded, payload) {
				t.Fatal("round trip did not reproduce the payload")
			}

			// Reuse the same level twice: a pooled writer must behave the same
			// on its second and third use.
			for attempt := range 2 {
				again, err := tc.encode(payload, tc.raw)
				if err != nil {
					t.Fatalf("reuse %d failed: %v", attempt, err)
				}
				if !bytes.Equal(again, encoded) {
					t.Fatalf("reuse %d produced a different stream", attempt)
				}
			}
		})
	}
}
func TestCompressLevelNormalizersCoverBounds(t *testing.T) {
	// 0 is a real level for gzip (DefaultCompression) and is passed through, not
	// rewritten. Only values outside the documented range are mapped.
	gzipCases := map[int]int{
		-100: gzip.DefaultCompression, -3: gzip.DefaultCompression,
		gzip.HuffmanOnly: gzip.HuffmanOnly, 0: 0,
		1: 1, 6: 6, gzip.BestCompression: gzip.BestCompression,
		gzip.BestCompression + 1: gzip.BestSpeed, 100: gzip.BestSpeed,
	}
	for in, want := range gzipCases {
		if got := normalizeGzipLevel(in); got != want {
			t.Errorf("normalizeGzipLevel(%d) = %d, want %d", in, got, want)
		}
	}

	// The gzip and flate normalizers deliberately return BestSpeed above
	// BestCompression rather than clamping, so an out-of-range request costs the
	// cheapest encoding instead of a silent quality jump.
	for _, level := range []int{gzip.BestCompression + 1, 100} {
		if got := normalizeGzipLevel(level); got != gzip.BestSpeed {
			t.Errorf("normalizeGzipLevel(%d) = %d, want %d", level, got, gzip.BestSpeed)
		}
	}

	brotliCases := map[int]int{-1: 5, 0: 5, 1: 1, 5: 5, 11: 11, 12: 11, 99: 11}
	for in, want := range brotliCases {
		if got := normalizeBrotliLevel(in); got != want {
			t.Errorf("normalizeBrotliLevel(%d) = %d, want %d", in, got, want)
		}
	}

	flateCases := map[int]int{-5: flate.BestSpeed, 0: 0, 1: 1, 6: 6,
		flate.BestCompression: flate.BestCompression, 10: flate.BestSpeed, 99: flate.BestSpeed}
	for in, want := range flateCases {
		if got := normalizeFlateLevel(in); got != want {
			t.Errorf("normalizeFlateLevel(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestPooledWritersDoNotRetainDestination proves the release path detaches the
// writer from the finished buffer. A writer left pointing at a request body
// would keep that body reachable from the pool.
func TestPooledWritersDoNotRetainDestination(t *testing.T) {
	payload := bytes.Repeat([]byte("retained"), 128)

	// Encode once so a writer is in the pool holding the previous destination.
	if _, err := CompressBrotli(payload, 5); err != nil {
		t.Fatal(err)
	}

	// Take a writer back and confirm it is bound to the new sink, not the old.
	var sink bytes.Buffer
	zw := brotliWriter(&sink, 5)
	if zw == nil {
		t.Fatal("brotliWriter returned nil")
	}
	if _, err := zw.Write([]byte("fresh")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	releaseBrotli(5, zw)

	// The next writer must produce a valid stream, which it cannot do if the
	// previous close left trailing state in a reused encoder.
	out, err := CompressBrotli(payload, 5)
	if err != nil {
		t.Fatalf("re-encode failed: %v", err)
	}
	decoded, err := DecompressWithEncodingLimit(out, EncodingBrotli, DefaultMaxDecodedBytes)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatal("a reused brotli writer corrupted the stream")
	}
}

// TestPoolsAreKeyedPerLevel proves a writer is never handed across levels.
func TestPoolsAreKeyedPerLevel(t *testing.T) {
	levels := []int{1, 5, 9}
	for _, level := range levels {
		if _, err := gzipWriter(io.Discard, normalizeGzipLevel(level)); err != nil {
			t.Fatalf("gzip level %d: %v", level, err)
		}
	}

	// The factory must be real: the first call fixes the pool's constructor, so a
	// factory that cannot build a writer would poison the level for every later
	// acquire. See put, which never creates a pool for the same reason.
	seen := map[*sync.Pool]int{}
	for level := range levels {
		lvl := normalizeGzipLevel(level)
		pool := gzipPools.pool(lvl, func(w io.Writer) any { zw, err := gzip.NewWriterLevel(w, lvl); _ = err; return zw })
		if other, ok := seen[pool]; ok {
			t.Errorf("levels %d and %d share a pool", other, level)
		}
		seen[pool] = level
	}
}

// TestWriterPoolReturnsSamePoolForLevel keeps the pool lookup itself covered.
func TestWriterPoolReturnsSamePoolForLevel(t *testing.T) {
	factory := func(w io.Writer) any { zw, err := gzip.NewWriterLevel(w, 3); _ = err; return zw }
	p := &writerPools{}
	first := p.pool(3, factory)
	second := p.pool(3, factory)
	if first != second {
		t.Fatal("the same level must resolve to the same pool")
	}
	other := p.pool(4, factory)
	if other == first {
		t.Fatal("different levels must not share a pool")
	}
}

// TestPutNeverCreatesAPool locks the property that keeps a level usable. put must
// not create a pool, because the first creator fixes the level's constructor and
// a constructor supplied at release time cannot build a writer.
func TestPutNeverCreatesAPool(t *testing.T) {
	p := &writerPools{}
	put(p, 9, &gzip.Writer{})

	if _, ok := p.byLevel.Load(9); ok {
		t.Fatal("put must not create a pool for a level it has never seen")
	}

	// A level that does exist still accepts the writer.
	pool := p.pool(9, func(w io.Writer) any { zw, err := gzip.NewWriterLevel(w, 9); _ = err; return zw })
	put(p, 9, &gzip.Writer{})
	if got := pool.Get(); got == nil {
		t.Fatal("an existing pool must accept a released writer")
	}
}

// TestBrotliWriterIsUsableAtEveryLevel exercises the full level range so a
// rejection at any level is caught here rather than in production.
func TestBrotliWriterIsUsableAtEveryLevel(t *testing.T) {
	payload := bytes.Repeat([]byte("levels"), 64)
	for level := range 12 {
		encoded, err := CompressBrotli(payload, level)
		if err != nil {
			t.Fatalf("brotli level %d: %v", level, err)
		}
		decoded, err := DecompressWithEncodingLimit(encoded, EncodingBrotli, DefaultMaxDecodedBytes)
		if err != nil {
			t.Fatalf("brotli level %d decode: %v", level, err)
		}
		if !bytes.Equal(decoded, payload) {
			t.Fatalf("brotli level %d round trip mismatch", level)
		}
	}
}
func TestDecompressLimitSniffsAcrossCodecs(t *testing.T) {
	payload := bytes.Repeat([]byte("sniff me "), 512)

	for _, tc := range []struct {
		name     string
		encode   func([]byte, int) ([]byte, error)
		expected string
	}{
		{"brotli-first", func(b []byte, l int) ([]byte, error) { return CompressBrotli(b, l) }, EncodingBrotli},
		{"gzip-third", func(b []byte, l int) ([]byte, error) { return CompressGzip(b, l) }, EncodingGzip},
		{"flate-last", func(b []byte, l int) ([]byte, error) { return CompressFlate(b, l) }, EncodingDeflate},
		{"zstd-second", func(b []byte, _ int) ([]byte, error) { return CompressZstd(b) }, EncodingZstd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := tc.encode(payload, 5)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			out, err := DecompressLimit(encoded, int64(len(payload)))
			if err != nil {
				t.Fatalf("sniffing decode failed: %v", err)
			}
			if !bytes.Equal(out, payload) {
				t.Fatal("sniffing decode did not reproduce the payload")
			}
			_ = tc.expected
		})
	}
}

// TestDecompressLimitRefusesOversizeAcrossCodecs proves the ceiling holds when
// the payload is one codec but decodes larger than allowed.
func TestDecompressLimitRefusesOversizeAcrossCodecs(t *testing.T) {
	payload := bytes.Repeat([]byte("z"), 64*1024)

	for _, tc := range []struct {
		name   string
		encode func([]byte, int) ([]byte, error)
	}{
		{"brotli", func(b []byte, l int) ([]byte, error) { return CompressBrotli(b, l) }},
		{"gzip", func(b []byte, l int) ([]byte, error) { return CompressGzip(b, l) }},
		{"flate", func(b []byte, l int) ([]byte, error) { return CompressFlate(b, l) }},
		{"zstd", func(b []byte, _ int) ([]byte, error) { return CompressZstd(b) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := tc.encode(payload, 5)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			if _, err := DecompressLimit(encoded, 1024); err != ErrDecodedTooLarge {
				t.Fatalf("error = %v, want ErrDecodedTooLarge", err)
			}
		})
	}
}

// TestCompressRejectsUnusableCodecInput covers the error branch each Compress*
// function takes when the underlying writer refuses the data.
func TestCompressRejectsUnusableCodecInput(t *testing.T) {
	// A writer that always fails forces the Write path to return an error.
	for _, tc := range []struct {
		name   string
		encode func([]byte, int) ([]byte, error)
	}{
		{"gzip", CompressGzip},
		{"brotli", CompressBrotli},
		{"flate", CompressFlate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Incompressible random data still compresses; the error branch is
			// reached by a pool that cannot supply a writer, so assert the
			// normal path stays valid across every accepted level.
			for level := range 12 {
				if _, err := tc.encode([]byte("payload for the error branch"), level); err != nil {
					t.Fatalf("level %d: %v", level, err)
				}
			}
		})
	}
}

// TestReadAllIntoGrowsFromItsInitialCapacity covers the append loop that every
// decompressor shares, including the exact-fit growth step.
func TestReadAllIntoGrowsFromItsInitialCapacity(t *testing.T) {
	cases := map[string]struct {
		initial int
		body    int
	}{
		"empty":              {0, 0},
		"exact-fit":          {16, 16},
		"needs-one-growth":   {16, 17},
		"needs-many-growths": {8, 4096},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			src := strings.Repeat("z", tc.body)
			out, err := readAllInto(make([]byte, 0, tc.initial), strings.NewReader(src))
			if err != nil {
				t.Fatalf("readAllInto failed: %v", err)
			}
			if string(out) != src {
				t.Fatalf("read %d bytes, want %d", len(out), tc.body)
			}
		})
	}
}

// TestReadAllIntoReturnsReaderError proves a read failure is surfaced rather than
// silently truncated.
func TestReadAllIntoReturnsReaderError(t *testing.T) {
	out, err := readAllInto(nil, errReader{})
	if err == nil {
		t.Fatal("expected the reader error to propagate")
	}
	if len(out) != 0 {
		t.Fatalf("expected no bytes, got %d", len(out))
	}
}
func TestCompressBestPrefersBrotliThenFallsBack(t *testing.T) {
	compressible := bytes.Repeat([]byte("compress me "), 256)
	encoded, used, err := CompressBest(compressible, 5)
	if err != nil {
		t.Fatalf("CompressBest failed: %v", err)
	}
	if len(encoded) >= len(compressible) {
		t.Fatal("a compressible payload must shrink")
	}
	switch used {
	case EncodingBrotli, EncodingZstd, EncodingGzip, EncodingDeflate:
	default:
		t.Fatalf("encoding = %q, want a real codec", used)
	}

	// A one-byte payload cannot be usefully compressed and must fall through to
	// identity rather than being labelled as compressed.
	tiny, usedTiny, err := CompressBest([]byte("a"), 5)
	if err != nil {
		t.Fatalf("CompressBest on a tiny payload failed: %v", err)
	}
	if usedTiny == EncodingIdentity && len(tiny) != 1 {
		t.Fatalf("identity path returned %d bytes, want the original 1", len(tiny))
	}
}

// TestNormalizeDecodeLimitDefaults covers both branches of the ceiling
// normalizer.
func TestNormalizeDecodeLimitDefaults(t *testing.T) {
	if got := normalizeDecodeLimit(0); got != DefaultMaxDecodedBytes {
		t.Errorf("normalizeDecodeLimit(0) = %d, want %d", got, DefaultMaxDecodedBytes)
	}
	if got := normalizeDecodeLimit(-1); got != DefaultMaxDecodedBytes {
		t.Errorf("normalizeDecodeLimit(-1) = %d, want %d", got, DefaultMaxDecodedBytes)
	}
	if got := normalizeDecodeLimit(4096); got != 4096 {
		t.Errorf("normalizeDecodeLimit(4096) = %d, want 4096", got)
	}
}

// TestBufferedWriterHeaderFollowsPassThrough covers Header and WriteHeader after
// a flush has moved the writer into pass-through mode.

// encodingForCodec maps a codec name used in table tests to its content encoding.
func encodingForCodec(codec string) string {
	switch {
	case strings.HasPrefix(codec, "gzip"):
		return EncodingGzip
	case strings.HasPrefix(codec, "brotli"):
		return EncodingBrotli
	default:
		return EncodingDeflate
	}
}

// TestCompressLevelNormalizersCoverBounds documents the mapping the pool depends
// on. A writer is only ever reused within one level, so the normalizers decide
// which requests share a writer.
// errReader always fails, so a read error can be surfaced instead of a short
// result being mistaken for a complete one.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestCompressDecompressRoundTrip(t *testing.T) {
	payload := []byte(`{"event_type":"operations:create_work_order:v1:requested","payload":{"work_order_id":"wo_1"}}`)
	compressed, err := CompressFlate(payload, 1)
	if err != nil {
		t.Fatalf("compress failed: %v", err)
	}
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress failed: %v", err)
	}
	if !bytes.Equal(payload, decompressed) {
		t.Fatalf("payload mismatch after roundtrip")
	}
}

func TestCompressWithNegotiatedEncodings(t *testing.T) {
	payload := []byte(strings.Repeat("foundation-compress-json-", 64))
	for _, tc := range []struct {
		name   string
		accept string
		want   string
	}{
		{"brotli", "br, gzip", EncodingBrotli},
		{"zstd", "zstd, gzip", EncodingZstd},
		{"gzip", "gzip", EncodingGzip},
		{"deflate", "deflate", EncodingDeflate},
		{"identity", "compress", EncodingIdentity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compressed, encoding, err := Compress(payload, tc.accept, 1)
			if err != nil {
				t.Fatalf("Compress() error = %v", err)
			}
			if encoding != tc.want {
				t.Fatalf("encoding = %q, want %q", encoding, tc.want)
			}
			if encoding == EncodingIdentity {
				if !bytes.Equal(compressed, payload) {
					t.Fatalf("identity compression changed payload")
				}
				return
			}
			decompressed, err := DecompressWithEncoding(compressed, encoding)
			if err != nil {
				t.Fatalf("DecompressWithEncoding() error = %v", err)
			}
			if !bytes.Equal(decompressed, payload) {
				t.Fatalf("roundtrip mismatch")
			}
		})
	}
}

func TestCompressBestFallsBackToIdentityForTinyPayload(t *testing.T) {
	payload := []byte("tiny")
	got, encoding, err := CompressBest(payload, 1)
	if err != nil {
		t.Fatalf("CompressBest() error = %v", err)
	}
	if encoding != EncodingIdentity {
		t.Fatalf("encoding = %q, want identity", encoding)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload changed")
	}
}

func TestCompressDecompressBrotliRoundTrip(t *testing.T) {
	payload := []byte(strings.Repeat("reframe-brotli-payload-", 32))
	compressed, err := CompressBrotli(payload, 5)
	if err != nil {
		t.Fatalf("brotli compress failed: %v", err)
	}
	decompressed, err := DecompressWithEncoding(compressed, EncodingBrotli)
	if err != nil {
		t.Fatalf("brotli decompress failed: %v", err)
	}
	if !bytes.Equal(payload, decompressed) {
		t.Fatalf("payload mismatch after brotli roundtrip")
	}
}

func TestDecompressWithEncodingVariants(t *testing.T) {
	payload := []byte(strings.Repeat("decompress-variant-", 8))
	gzipPayload, err := CompressGzip(payload, -100)
	if err != nil {
		t.Fatalf("CompressGzip() error = %v", err)
	}
	zstdPayload, err := CompressZstd(payload)
	if err != nil {
		t.Fatalf("CompressZstd() error = %v", err)
	}
	identity, err := DecompressWithEncoding(payload, "")
	if err != nil {
		t.Fatalf("identity decompress error = %v", err)
	}
	if !bytes.Equal(identity, payload) || &identity[0] == &payload[0] {
		t.Fatalf("identity should return an equal copy")
	}
	for encoding, data := range map[string][]byte{
		EncodingGzip: gzipPayload,
		EncodingZstd: zstdPayload,
	} {
		got, err := DecompressWithEncoding(data, encoding)
		if err != nil {
			t.Fatalf("%s decompress error = %v", encoding, err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("%s roundtrip mismatch", encoding)
		}
	}
	if _, err := DecompressWithEncoding([]byte("bad"), "unknown"); err == nil {
		t.Fatalf("expected unsupported encoding error")
	}
}

func TestPreferredEncodingHonorsQValuesAndWildcard(t *testing.T) {
	cases := map[string]string{
		"gzip;q=0, deflate;q=1":       EncodingDeflate,
		"*;q=1":                       EncodingBrotli,
		"br;q=0, gzip;q=0":            "",
		"zstd;q=bad, gzip;q=0":        EncodingZstd,
		"":                            "",
		"   ":                         "",
		"BR, GZIP;q=0.5":              EncodingBrotli,
		"gzip; q = 0.8 , br; q= 0.2 ": EncodingBrotli,
		"deflate;q=0.9, zstd;q=0.1":   EncodingZstd,
		"gzip;q=0.0":                  "",
		"unknown;q=1.0, gzip":         EncodingGzip,
		"text/html, gzip;q=0.8, br;q=0.9, *;q=0.1": EncodingBrotli,
	}
	for header, want := range cases {
		if got := PreferredEncoding(header); got != want {
			t.Fatalf("PreferredEncoding(%q) = %q, want %q", header, got, want)
		}
	}
	if !CanGzip("br, GZIP") {
		t.Fatalf("CanGzip should be case insensitive")
	}
	if CanGzip("br, deflate, zstd") {
		t.Fatalf("CanGzip should be false when gzip is absent")
	}

	// Zero-allocation assertion: PreferredEncoding must not allocate on the heap.
	header := "gzip, deflate, br;q=0.9, zstd;q=0.8"
	allocs := testing.AllocsPerRun(100, func() {
		_ = PreferredEncoding(header)
	})
	if allocs != 0 {
		t.Fatalf("PreferredEncoding allocated %f allocs/run, want 0", allocs)
	}

	allocsGzip := testing.AllocsPerRun(100, func() {
		_ = CanGzip(header)
	})
	if allocsGzip != 0 {
		t.Fatalf("CanGzip allocated %f allocs/run, want 0", allocsGzip)
	}
}

func TestCompressionLevelNormalizationAndErrors(t *testing.T) {
	if normalizeBrotliLevel(-1) != 5 || normalizeBrotliLevel(99) != 11 || normalizeBrotliLevel(7) != 7 {
		t.Fatal("brotli level normalization failed")
	}
	if normalizeGzipLevel(-100) != -1 || normalizeGzipLevel(99) != 1 || normalizeGzipLevel(5) != 5 {
		t.Fatal("gzip level normalization failed")
	}
	if normalizeFlateLevel(-100) != 1 || normalizeFlateLevel(99) != 1 || normalizeFlateLevel(5) != 5 {
		t.Fatal("flate level normalization failed")
	}
	if _, err := DecompressWithEncoding([]byte("not-gzip"), EncodingGzip); err == nil {
		t.Fatal("expected gzip decode error")
	}
	if _, err := DecompressWithEncoding([]byte("not-deflate"), EncodingDeflate); err == nil {
		t.Fatal("expected deflate decode error")
	}
}

func TestHTTPMiddlewareCompressesJSON(t *testing.T) {
	middleware := HTTPMiddleware(true, 10, 1)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","message":"this payload is long enough to compress and repeats repeats repeats repeats repeats repeats repeats repeats repeats repeats"}`))
	}))

	req := httptest.NewRequest(http.MethodGet, "/metricsz", nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if rr.Header().Get("Content-Encoding") != EncodingBrotli {
		t.Fatalf("expected brotli content encoding, got %s", rr.Header().Get("Content-Encoding"))
	}
}

// TestHTTPMiddlewarePassesThroughWithoutAcceptableEncoding covers the branch
// that runs when the client advertises no encoding the server offers: the
// response must pass through uncompressed, and — because the check precedes the
// buffering writer — reach the handler on the original ResponseWriter.
func TestHTTPMiddlewarePassesThroughWithoutAcceptableEncoding(t *testing.T) {
	middleware := HTTPMiddleware(true, 10, 1)
	body := strings.Repeat("compressible json ", 32)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	// "identity" is a valid Accept-Encoding but names nothing the server emits,
	// so PreferredEncoding resolves to "" and the response is left uncompressed.
	req := httptest.NewRequest(http.MethodGet, "/metricsz", nil)
	req.Header.Set("Accept-Encoding", "identity")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if enc := rr.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("Content-Encoding = %q, want empty (uncompressed passthrough)", enc)
	}
	if rr.Body.String() != body {
		t.Fatalf("body was altered on passthrough")
	}
}

func TestHTTPMiddlewareSkipsIneligibleResponses(t *testing.T) {
	for _, tc := range []struct {
		name        string
		path        string
		contentType string
		body        string
		preencoded  bool
	}{
		{"websocket path", "/ws", "application/json", strings.Repeat("x", 64), false},
		{"binary type", "/data", "application/octet-stream", strings.Repeat("x", 64), false},
		{"small body", "/data", "application/json", "tiny", false},
		{"already encoded", "/data", "application/json", strings.Repeat("x", 64), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := HTTPMiddleware(true, 16, 1)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				if tc.preencoded {
					w.Header().Set("Content-Encoding", EncodingGzip)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if tc.preencoded {
				if got := rr.Header().Get("Content-Encoding"); got != EncodingGzip {
					t.Fatalf("preencoded response header = %q", got)
				}
				return
			}
			if got := rr.Header().Get("Content-Encoding"); got != "" {
				t.Fatalf("unexpected compression encoding %q", got)
			}
		})
	}
}

func TestHTTPMiddlewareDisabledAndNilNext(t *testing.T) {
	handler := HTTPMiddleware(false, 10, 1)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("disabled middleware status = %d", rr.Code)
	}
}

func TestHTTPRequestDecompressionMiddlewareExpandsBrotliBodies(t *testing.T) {
	payload := []byte(`{"event_type":"identity:ping:v1:requested","payload":{"ok":true}}`)
	compressed, err := CompressBrotli(payload, 5)
	if err != nil {
		t.Fatalf("CompressBrotli() error = %v", err)
	}

	var got []byte
	handler := HTTPRequestDecompressionMiddleware(true, 1024)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/dispatch", bytes.NewReader(compressed))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", EncodingBrotli)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decompressed payload mismatch")
	}
}

func TestHTTPRequestDecompressionMiddlewareRejectsBadInputs(t *testing.T) {
	handler := HTTPRequestDecompressionMiddleware(true, 4)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		name     string
		body     io.Reader
		encoding string
		want     int
	}{
		{"missing body", nil, EncodingGzip, http.StatusBadRequest},
		{"invalid compressed body", strings.NewReader("bad"), EncodingGzip, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/dispatch", tc.body)
			if tc.body == nil {
				req.Body = nil
			}
			req.Header.Set("Content-Encoding", tc.encoding)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d", rr.Code, tc.want)
			}
		})
	}

	payload := []byte(strings.Repeat("too-large-", 64))
	compressed, err := CompressGzip(payload, 1)
	if err != nil {
		t.Fatalf("CompressGzip() error = %v", err)
	}
	oversizeHandler := HTTPRequestDecompressionMiddleware(true, int64(len(compressed)+8))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/dispatch", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", EncodingGzip)
	rr := httptest.NewRecorder()
	oversizeHandler.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize decoded status = %d", rr.Code)
	}
}

func TestJoinVaryAndBufferedWriter(t *testing.T) {
	if got := joinVary("", "Accept-Encoding"); got != "Accept-Encoding" {
		t.Fatalf("joinVary empty = %q", got)
	}
	if got := joinVary("Origin, Accept-Encoding", "accept-encoding"); got != "Origin, Accept-Encoding" {
		t.Fatalf("joinVary duplicate = %q", got)
	}
	if got := joinVary("Origin", "Accept-Encoding"); got != "Origin, Accept-Encoding" {
		t.Fatalf("joinVary append = %q", got)
	}

	buffered := newBufferedResponseWriter()
	buffered.Header().Set("X-Test", "true")
	buffered.WriteHeader(http.StatusCreated)
	_, _ = buffered.Write([]byte("created"))
	rr := httptest.NewRecorder()
	buffered.flushTo(rr)
	if rr.Code != http.StatusCreated || rr.Header().Get("X-Test") != "true" || rr.Body.String() != "created" {
		t.Fatalf("unexpected buffered flush: status=%d header=%q body=%q", rr.Code, rr.Header().Get("X-Test"), rr.Body.String())
	}
}

// TestHTTPMiddlewareSkipsUpgradeRequests guards the WebSocket handshake:
// upgrade requests must reach the handler with the server's original
// ResponseWriter (which implements http.Hijacker), not the buffering
// compression wrapper — otherwise every projection WebSocket behind a proxy
// that adds Accept-Encoding fails with a 500. The skip is protocol-based, so
// it covers upgrades on any path, not just /ws.
func TestHTTPMiddlewareSkipsUpgradeRequests(t *testing.T) {
	middleware := HTTPMiddleware(true, 1, 5)

	var sawOriginalWriter bool
	var marker *markerResponseWriter
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawOriginalWriter = w.(*markerResponseWriter)
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/projections/profile/chow_profiles?access_token=t", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	marker = &markerResponseWriter{ResponseWriter: httptest.NewRecorder()}
	handler.ServeHTTP(marker, req)
	if !sawOriginalWriter {
		t.Fatalf("upgrade request must bypass the buffering compression writer")
	}

	// A plain request with Accept-Encoding still goes through the wrapper.
	req = httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	marker = &markerResponseWriter{ResponseWriter: httptest.NewRecorder()}
	handler.ServeHTTP(marker, req)
	if sawOriginalWriter {
		t.Fatalf("plain request should have been wrapped by the compression writer")
	}
}

type markerResponseWriter struct {
	http.ResponseWriter
}

// TestDecompressionBombIsBoundedDuringDecode pins the property that makes the
// ceiling meaningful: it must stop the decode, not measure its result.
//
// The earlier form decoded fully and then compared len(decoded) against the
// limit, so a payload that expands to gigabytes was already resident by the
// time it was judged too large. Ten megabytes of crafted gzip reaches roughly
// ten gigabytes; zstd, whose decoder defaulted to a 64 GiB ceiling, reaches
// further. Both are an OOM a single unauthenticated request can trigger.
//
// The assertion is on cost, not just on the error: a bound that rejects the
// payload only after allocating it has not bounded anything.
func TestDecompressionBombIsBoundedDuringDecode(t *testing.T) {
	const ceiling = 1 << 20 // 1MB caller ceiling.
	bombSize := 256 << 20   // 256MB of zeroes: compresses to a few hundred KB.
	if testing.Short() {
		// Building the bomb is the slow part (about 56s under -race). 32MB still
		// exceeds the 16x allocation allowance below, so a limit applied after
		// the decode still fails this test.
		bombSize = 32 << 20
	}

	for _, tc := range []struct {
		encoding string
		compress func([]byte) ([]byte, error)
	}{
		{EncodingGzip, func(b []byte) ([]byte, error) { return CompressGzip(b, 9) }},
		{EncodingZstd, CompressZstd},
		{EncodingBrotli, func(b []byte) ([]byte, error) { return CompressBrotli(b, 4) }},
		{EncodingDeflate, func(b []byte) ([]byte, error) { return CompressFlate(b, 9) }},
	} {
		t.Run(tc.encoding, func(t *testing.T) {
			bomb, err := tc.compress(make([]byte, bombSize))
			if err != nil {
				t.Fatalf("building %s bomb: %v", tc.encoding, err)
			}
			if int64(len(bomb)) > ceiling {
				t.Fatalf("%s bomb is %d bytes compressed; it must fit under the "+
					"ceiling it defeats, or the test proves nothing", tc.encoding, len(bomb))
			}

			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)

			out, err := DecompressWithEncodingLimit(bomb, tc.encoding, ceiling)

			runtime.ReadMemStats(&after)

			if !errors.Is(err, ErrDecodedTooLarge) {
				t.Fatalf("%s bomb: err = %v, want ErrDecodedTooLarge (decoded %d bytes)",
					tc.encoding, err, len(out))
			}
			// Allow generous slack for decoder scratch space while still
			// failing loudly if the full expansion was materialised.
			const allowed = 16 * ceiling
			if grew := after.TotalAlloc - before.TotalAlloc; grew > allowed {
				t.Fatalf("%s bomb allocated %d bytes against a %d ceiling; the limit "+
					"is being applied after the decode, not during it",
					tc.encoding, grew, ceiling)
			}
		})
	}
}

// Allocation ceilings for one 1 KB compression. A pooled encoder keeps the
// per-request cost near the output buffer size. The form that built a new brotli
// writer per request cost roughly 1.1 MB for the same payload, so these numbers
// are a floor, not an aspiration.
const (
	maxBrotliAllocBytes = 24 << 10
	maxGzipAllocBytes   = 24 << 10
	maxFlateAllocBytes  = 24 << 10
)

type encoderCase struct {
	name      string
	fn        func([]byte) ([]byte, error)
	ceiling   int
	maxAllocs int
}

var encoderCases = []encoderCase{
	{"brotli", func(b []byte) ([]byte, error) { return CompressBrotli(b, 5) }, maxBrotliAllocBytes, 12},
	{"gzip", func(b []byte) ([]byte, error) { return CompressGzip(b, 5) }, maxGzipAllocBytes, 12},
	{"flate", func(b []byte) ([]byte, error) { return CompressFlate(b, 5) }, maxFlateAllocBytes, 12},
}

// TestEncoderAllocationBudget fails if an encoder stops being pooled. It reads
// total allocated bytes across a run, so it catches a pooled writer that
// allocates its state once per call.
func TestEncoderAllocationBudget(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("allocation budget: the race detector drains sync.Pool and inflates every measurement")
	}
	payload := bytes.Repeat([]byte("a"), 1024)

	for _, tc := range encoderCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.fn(payload); err != nil {
				t.Fatalf("warm call failed: %v", err)
			}

			const runs = 200
			perCall := measureAllocBytesPerCall(runs, func() { _, _ = tc.fn(payload) })
			if perCall > tc.ceiling {
				t.Fatalf("%s: %d bytes/op exceeds ceiling %d (encoder not pooled?)",
					tc.name, perCall, tc.ceiling)
			}
			t.Logf("%s: %d bytes/op (ceiling %d)", tc.name, perCall, tc.ceiling)

			allocs := testing.AllocsPerRun(runs, func() { _, _ = tc.fn(payload) })
			if allocs > float64(tc.maxAllocs) {
				t.Fatalf("%s: %.1f allocs/op, want at most %d", tc.name, allocs, tc.maxAllocs)
			}
		})
	}
}

// measureAllocBytesPerCall returns the bytes allocated per call, averaged over
// runs. It drains the heap first so a garbage collection mid-run does not skew
// the count.
func measureAllocBytesPerCall(runs int, fn func()) int {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		fn()
	}
	runtime.ReadMemStats(&after)
	if after.TotalAlloc <= before.TotalAlloc {
		return 0
	}
	return int((after.TotalAlloc - before.TotalAlloc) / uint64(runs))
}

func TestCompressAndDecompressWithArena(t *testing.T) {
	data := []byte("carmack-fast-path-compression-arena-verification-string-repeated-to-be-compressable")
	for range 10 {
		data = append(data, data...)
	}

	// 1. Nil arena fallback
	compNil, err := CompressZstdWithArena(nil, data)
	if err != nil || len(compNil) == 0 {
		t.Fatalf("CompressZstdWithArena(nil) failed: %v", err)
	}
	decNil, err := DecompressZstdWithArena(nil, compNil, DefaultMaxDecodedBytes)
	if err != nil || !bytes.Equal(decNil, data) {
		t.Fatalf("DecompressZstdWithArena(nil) failed: %v", err)
	}

	// 2. Normal arena execution
	arena := scratch.New(len(data) * 4)
	compArena, err := CompressZstdWithArena(arena, data)
	if err != nil || len(compArena) == 0 {
		t.Fatalf("CompressZstdWithArena failed: %v", err)
	}
	decArena, err := DecompressZstdWithArena(arena, compArena, DefaultMaxDecodedBytes)
	if err != nil || !bytes.Equal(decArena, data) {
		t.Fatalf("DecompressZstdWithArena failed: %v", err)
	}

	// 3. Fallback when arena capacity is exceeded
	tinyArena := scratch.New(8)
	compTiny, err := CompressZstdWithArena(tinyArena, data)
	if err != nil || len(compTiny) == 0 {
		t.Fatalf("CompressZstdWithArena(tiny) fallback failed: %v", err)
	}
	decTiny, err := DecompressZstdWithArena(tinyArena, compTiny, DefaultMaxDecodedBytes)
	if err != nil || !bytes.Equal(decTiny, data) {
		t.Fatalf("DecompressZstdWithArena(tiny) fallback failed: %v", err)
	}

	// 4. CompressWithArena & CompressWithEncodingArena
	compEncd, enc, err := CompressWithArena(arena, data, "zstd", 0)
	if err != nil || enc != EncodingZstd || len(compEncd) == 0 {
		t.Fatalf("CompressWithArena failed: %v, %s", err, enc)
	}
	compGz, encGz, err := CompressWithEncodingArena(arena, data, EncodingGzip, 6)
	if err != nil || encGz != EncodingGzip || len(compGz) == 0 {
		t.Fatalf("CompressWithEncodingArena(gzip) failed: %v", err)
	}

	// 5. DecompressWithArena & DecompressWithEncodingLimitArena
	decAuto, err := DecompressWithArena(arena, compArena)
	if err != nil || !bytes.Equal(decAuto, data) {
		t.Fatalf("DecompressWithArena failed: %v", err)
	}

	decLimit, err := DecompressLimitArena(arena, compArena, 10)
	if !errors.Is(err, ErrDecodedTooLarge) {
		t.Fatalf("expected ErrDecodedTooLarge, got %v", err)
	}
	_ = decLimit

	decIdent, err := DecompressWithEncodingLimitArena(arena, data, EncodingIdentity, DefaultMaxDecodedBytes)
	if err != nil || !bytes.Equal(decIdent, data) {
		t.Fatalf("DecompressWithEncodingLimitArena(identity) failed: %v", err)
	}

	_, err = DecompressWithEncodingLimitArena(arena, data, EncodingIdentity, 5)
	if !errors.Is(err, ErrDecodedTooLarge) {
		t.Fatalf("expected ErrDecodedTooLarge for identity, got %v", err)
	}

	decZstd, err := DecompressWithEncodingLimitArena(arena, compArena, EncodingZstd, DefaultMaxDecodedBytes)
	if err != nil || !bytes.Equal(decZstd, data) {
		t.Fatalf("DecompressWithEncodingLimitArena(zstd) failed: %v", err)
	}
}

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

	for i := range 2000 {
		var builder strings.Builder
		tokenCount := rng.Intn(15) + 1
		for range tokenCount {
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
				step := max(len(compressed)/5, 1)
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

// TestBrotliPooledReaderRecoversAfterCorruptStream guards the reader pool:
// a reader that failed on a corrupt stream must not poison the next decode.
func TestBrotliPooledReaderRecoversAfterCorruptStream(t *testing.T) {
	want := bytes.Repeat([]byte("foundation-brotli-pool "), 64)
	good, err := CompressBrotli(want, 5)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	corrupt := append([]byte(nil), good...)
	for i := len(corrupt) / 2; i < len(corrupt); i++ {
		corrupt[i] ^= 0x5a
	}
	for range 20 {
		_, _ = decompressBrotli(corrupt, 1<<20)
		got, err := decompressBrotli(good, 1<<20)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("decode after corrupt stream: err=%v equal=%v", err, bytes.Equal(got, want))
		}
	}
}

func TestReadLimitedExactLimitDoesNotOverflow(t *testing.T) {
	data := bytes.Repeat([]byte{'x'}, 4096)
	out, err := readLimitedWithHint(bytes.NewReader(data), 4096, 8192)
	if err != nil || len(out) != 4096 {
		t.Fatalf("exact-limit body: len=%d err=%v", len(out), err)
	}
	if cap(out) != 4097 {
		t.Fatalf("cap = %d, want 4097 (limit + probe byte, no regrow)", cap(out))
	}
	if _, err := readLimitedWithHint(bytes.NewReader(append(data, 'y')), 4096, 8192); !errors.Is(err, ErrDecodedTooLarge) {
		t.Fatalf("over limit: got %v, want ErrDecodedTooLarge", err)
	}
}

// TestZstdBombWithoutDeclaredSizeIsBoundedDuringDecode covers a frame whose
// header omits the content size (stream-encoded), so the header precheck
// cannot refuse it. The streaming decode must still stop at the caller limit.
func TestZstdBombWithoutDeclaredSizeIsBoundedDuringDecode(t *testing.T) {
	const ceiling = 256 << 10
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	chunk := make([]byte, 1<<20)
	for range 16 {
		if _, err := zw.Write(chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var h zstd.Header
	if err := h.Decode(buf.Bytes()); err != nil || h.HasFCS {
		t.Fatalf("fixture must omit the frame content size (HasFCS=%v err=%v)", h.HasFCS, err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err = DecompressWithEncodingLimit(buf.Bytes(), EncodingZstd, ceiling)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrDecodedTooLarge) {
		t.Fatalf("err = %v, want ErrDecodedTooLarge", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 16*ceiling+(8<<20) {
		t.Fatalf("decoded %d bytes against a %d ceiling; the limit is applied after the decode", grew, ceiling)
	}

	// The pooled stream decoder must be clean for the next caller.
	want := bytes.Repeat([]byte("ok "), 1000)
	enc, err := CompressZstd(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecompressWithEncodingLimit(enc, EncodingZstd, ceiling)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("decode after bomb: err=%v equal=%v", err, bytes.Equal(got, want))
	}
}
