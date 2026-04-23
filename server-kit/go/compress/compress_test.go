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
