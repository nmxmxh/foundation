package compress

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
		"gzip;q=0, deflate;q=1": EncodingDeflate,
		"*;q=1":                 EncodingBrotli,
		"br;q=0, gzip;q=0":      "",
		"zstd;q=bad, gzip;q=0":  EncodingZstd,
		"":                      "",
	}
	for header, want := range cases {
		if got := PreferredEncoding(header); got != want {
			t.Fatalf("PreferredEncoding(%q) = %q, want %q", header, got, want)
		}
	}
	if !CanGzip("br, GZIP") {
		t.Fatalf("CanGzip should be case insensitive")
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
