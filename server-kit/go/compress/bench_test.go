package compress

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkCompressLargeBatch(b *testing.B) {
	// Create a 1MB payload of semi-random data to simulate a large batch envelope
	// (Some repetition to allow compression to actually work)
	data := compressionFixture()

	b.Run("Brotli-Q4", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = CompressBrotli(data, 4)
		}
	})

	b.Run("Zstd-Fastest", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = CompressZstd(data)
		}
	})
}

func BenchmarkDecompressLargeBatch(b *testing.B) {
	data := compressionFixture()

	brData, _ := CompressBrotli(data, 4)
	zstdData, _ := CompressZstd(data)

	b.Run("Brotli", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = decompressBrotli(brData)
		}
	})

	b.Run("Zstd", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = decompressZstd(zstdData)
		}
	})
}

// BenchmarkHTTPMiddlewareDispatch measures the middleware's per-request
// dispatch cost on its three routes: an upgrade handshake (bypassed via the
// protocol check, must stay allocation-free), a small response below minBytes
// (buffered then flushed uncompressed), and a compressible response (buffered
// and re-encoded). Every projection WebSocket handshake and every API request
// crosses this dispatch, so its overhead is a per-request tax.
func BenchmarkHTTPMiddlewareDispatch(b *testing.B) {
	payload := compressionFixture()[:16*1024]
	handler := func(status int, body []byte) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(body)
		})
	}

	b.Run("upgrade-bypass", func(b *testing.B) {
		wrapped := HTTPMiddleware(true, 1024, 4)(handler(http.StatusSwitchingProtocols, nil))
		req := httptest.NewRequest(http.MethodGet, "/v1/projections/signals/ticks", nil)
		req.Header.Set("Accept-Encoding", "gzip, br")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		rec := httptest.NewRecorder()
		b.ReportAllocs()
		for b.Loop() {
			wrapped.ServeHTTP(rec, req)
		}
	})

	b.Run("small-uncompressed", func(b *testing.B) {
		wrapped := HTTPMiddleware(true, 1024, 4)(handler(http.StatusOK, payload[:128]))
		req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
		req.Header.Set("Accept-Encoding", "gzip, br")
		b.ReportAllocs()
		for b.Loop() {
			wrapped.ServeHTTP(httptest.NewRecorder(), req)
		}
	})

	b.Run("compressible-16k", func(b *testing.B) {
		wrapped := HTTPMiddleware(true, 1024, 4)(handler(http.StatusOK, payload))
		req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
		req.Header.Set("Accept-Encoding", "br")
		b.ReportAllocs()
		for b.Loop() {
			wrapped.ServeHTTP(httptest.NewRecorder(), req)
		}
	})
}

func TestCompressionRatio(t *testing.T) {
	data := compressionFixture()

	brData, err := CompressBrotli(data, 4)
	if err != nil {
		t.Fatalf("brotli compress failed: %v", err)
	}
	zstdData, err := CompressZstd(data)
	if err != nil {
		t.Fatalf("zstd compress failed: %v", err)
	}

	if len(brData) >= len(data) {
		t.Fatalf("brotli did not reduce deterministic fixture: original=%d compressed=%d", len(data), len(brData))
	}
	if len(zstdData) >= len(data) {
		t.Fatalf("zstd did not reduce deterministic fixture: original=%d compressed=%d", len(data), len(zstdData))
	}
}

func compressionFixture() []byte {
	data := make([]byte, 1024*1024)
	for i := range data[:512*1024] {
		data[i] = byte((i*31 + i/7) % 251)
	}
	copy(data[512*1024:], data[:512*1024])
	return data
}
