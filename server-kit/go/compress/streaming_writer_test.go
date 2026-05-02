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
