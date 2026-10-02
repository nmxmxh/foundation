package compress

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestStreamingCompressorCloseWithoutEncoder(t *testing.T) {
	var s StreamingCompressor
	if err := s.Close(); err != nil {
		t.Fatalf("Close on an empty compressor = %v, want nil", err)
	}
}

// TestStreamingCompressorFlushPassesThrough exercises the Flush chain: the codec
// is flushed where it can be, and the downstream writer is flushed either way.
func TestStreamingCompressorFlushPassesThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	s, ok := NewStreamingCompressor(rec, req, 5)
	if !ok {
		t.Fatal("NewStreamingCompressor refused a gzip client")
	}
	if _, err := s.Write([]byte("payload")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	s.Flush()
	if err := s.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if !rec.Flushed {
		t.Error("the downstream recorder must have been flushed")
	}
	if rec.Header().Get("Content-Encoding") != EncodingGzip {
		t.Errorf("Content-Encoding = %q, want %q", rec.Header().Get("Content-Encoding"), EncodingGzip)
	}
}

// TestNewStreamingCompressorRefusesUnacceptable covers the refusal branch.
// A bare wildcard is deliberately absent: PreferredEncoding maps "*" to brotli,
// so a client offering only the wildcard is satisfied.
func TestNewStreamingCompressorRefusesUnacceptable(t *testing.T) {
	for _, ae := range []string{"", "identity", "  "} {
		req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
		if ae != "" {
			req.Header.Set("Accept-Encoding", ae)
		}
		if _, ok := NewStreamingCompressor(httptest.NewRecorder(), req, 5); ok {
			t.Errorf("Accept-Encoding %q: expected a refusal", ae)
		}
	}

	// The wildcard path is accepted and negotiates the preferred codec.
	wildcard := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	wildcard.Header.Set("Accept-Encoding", "*")
	s, ok := NewStreamingCompressor(httptest.NewRecorder(), wildcard, 5)
	if !ok {
		t.Fatal("a wildcard Accept-Encoding must be satisfied")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

// TestStreamingCompressorWriteHeaderSetsEncodingOnce covers the header path.
func TestStreamingCompressorWriteHeaderSetsEncodingOnce(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	s, ok := NewStreamingCompressor(rec, req, 5)
	if !ok {
		t.Fatal("NewStreamingCompressor refused a gzip client")
	}
	s.WriteHeader(http.StatusOK)
	if _, err := s.Write([]byte("body")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	out, err := DecompressWithEncodingLimit(rec.Body.Bytes(), EncodingGzip, DefaultMaxDecodedBytes)
	if err != nil {
		t.Fatalf("decompress failed: %v", err)
	}
	if string(out) != "body" {
		t.Fatalf("round trip = %q, want %q", out, "body")
	}
}

func TestStreamingCompressor_Brotli(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "br")

	sc, ok := NewStreamingCompressor(rr, req, 5)
	if !ok {
		t.Fatal("expected StreamingCompressor to be created")
	}

	payload := []byte("streaming-data-payload-that-should-be-compressed")
	_, err := sc.Write(payload)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	err = sc.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	if rr.Header().Get("Content-Encoding") != EncodingBrotli {
		t.Errorf("expected br encoding, got %s", rr.Header().Get("Content-Encoding"))
	}

	// Verify decompression
	reader := brotli.NewReader(rr.Body)
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decompress failed: %v", err)
	}

	if !bytes.Equal(payload, decompressed) {
		t.Errorf("payload mismatch: expected %s, got %s", payload, decompressed)
	}
}

func TestStreamingCompressor_Gzip(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	sc, ok := NewStreamingCompressor(rr, req, 1)
	if !ok {
		t.Fatal("expected StreamingCompressor to be created")
	}

	payload := []byte("gzip-streaming-data")
	_, err := sc.Write(payload)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	_ = sc.Close()

	if rr.Header().Get("Content-Encoding") != EncodingGzip {
		t.Errorf("expected gzip encoding, got %s", rr.Header().Get("Content-Encoding"))
	}

	reader, err := gzip.NewReader(rr.Body)
	if err != nil {
		t.Fatalf("new gzip reader failed: %v", err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("decompress failed: %v", err)
	}

	if !bytes.Equal(payload, decompressed) {
		t.Errorf("payload mismatch")
	}
}

func TestStreamingCompressor_DeflateAndHeader(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "deflate")
	sc, ok := NewStreamingCompressor(rr, req, 100)
	if !ok {
		t.Fatal("expected StreamingCompressor to be created")
	}
	sc.Header().Set("X-Stream", "true")
	sc.WriteHeader(http.StatusCreated)
	_, _ = sc.Write([]byte("deflate-streaming-data"))
	if err := sc.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Header().Get("X-Stream") != "true" {
		t.Fatalf("missing passthrough header")
	}
	if rr.Header().Get("Content-Length") != "" {
		t.Fatalf("content length should be removed")
	}
}

func TestStreamingCompressor_NoCompression(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// No Accept-Encoding

	_, ok := NewStreamingCompressor(rr, req, 5)
	if ok {
		t.Error("expected StreamingCompressor NOT to be created when no encoding is accepted")
	}
}

func TestStreamingCompressor_Flush(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")

	sc, ok := NewStreamingCompressor(rr, req, 1)
	if !ok {
		t.Fatal("expected StreamingCompressor to be created")
	}

	payload := []byte("flushed-data")
	_, _ = sc.Write(payload)
	sc.Flush()

	if rr.Body.Len() == 0 {
		t.Error("expected data to be flushed to the response recorder")
	}
	_ = sc.Close()
}
