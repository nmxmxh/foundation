package compress

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

// HTTPMiddleware applies negotiated response compression to eligible responses.
func HTTPMiddleware(enabled bool, minBytes, level int) func(http.Handler) http.Handler {
	if minBytes <= 0 {
		minBytes = 1024
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled || next == nil {
				next.ServeHTTP(w, r)
				return
			}
			encoding := PreferredEncoding(r.Header.Get("Accept-Encoding"))
			if encoding == "" || shouldSkipCompressionPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			capture := newBufferedResponseWriter()
			next.ServeHTTP(capture, r)

			payload := capture.body.Bytes()
			if len(payload) < minBytes || !isCompressibleContentType(capture.Header().Get("Content-Type")) || capture.Header().Get("Content-Encoding") != "" {
				capture.flushTo(w)
				return
			}

			compressed, usedEncoding, err := Compress(payload, r.Header.Get("Accept-Encoding"), level)
			if err != nil || usedEncoding == "" || usedEncoding == EncodingIdentity || len(compressed) >= len(payload) {
				capture.flushTo(w)
				return
			}

			for key, values := range capture.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.Header().Set("Content-Encoding", usedEncoding)
			w.Header().Set("Vary", joinVary(w.Header().Get("Vary"), "Accept-Encoding"))
			w.Header().Del("Content-Length")

			status := capture.status
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			_, _ = w.Write(compressed)
		})
	}
}

func HTTPRequestDecompressionMiddleware(enabled bool, maxDecodedBytes int64) func(http.Handler) http.Handler {
	if maxDecodedBytes <= 0 {
		maxDecodedBytes = 10 * 1024 * 1024
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled || next == nil {
				next.ServeHTTP(w, r)
				return
			}
			encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
			if encoding == "" || encoding == EncodingIdentity {
				next.ServeHTTP(w, r)
				return
			}
			if r.Body == nil {
				http.Error(w, "request body is required", http.StatusBadRequest)
				return
			}
			payload, err := io.ReadAll(io.LimitReader(r.Body, maxDecodedBytes+1))
			_ = r.Body.Close()
			if err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			decoded, err := DecompressWithEncoding(payload, encoding)
			if err != nil {
				http.Error(w, "invalid compressed request body", http.StatusBadRequest)
				return
			}
			if int64(len(decoded)) > maxDecodedBytes {
				http.Error(w, "request body too large after decompression", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(decoded))
			r.ContentLength = int64(len(decoded))
			r.Header.Del("Content-Encoding")
			r.Header.Set("X-Original-Content-Encoding", encoding)
			next.ServeHTTP(w, r)
		})
	}
}

func shouldSkipCompressionPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	return path == "/ws" || strings.HasPrefix(path, "/ws?")
}

func isCompressibleContentType(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if ct == "" {
		return true
	}
	compressible := []string{
		"application/wasm",
		"application/json",
		"text/",
		"application/javascript",
		"application/x-capnp",
		"application/xml",
		"application/problem+json",
	}
	for _, prefix := range compressible {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	return false
}

func joinVary(existing, value string) string {
	existing = strings.TrimSpace(existing)
	value = strings.TrimSpace(value)
	if existing == "" {
		return value
	}
	parts := strings.SplitSeq(existing, ",")
	for part := range parts {
		if strings.EqualFold(strings.TrimSpace(part), value) {
			return existing
		}
	}
	return existing + ", " + value
}

type bufferedResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{
		header: make(http.Header),
	}
}

func (b *bufferedResponseWriter) Header() http.Header {
	return b.header
}

func (b *bufferedResponseWriter) Write(data []byte) (int, error) {
	return b.body.Write(data)
}

func (b *bufferedResponseWriter) WriteHeader(statusCode int) {
	b.status = statusCode
}

func (b *bufferedResponseWriter) flushTo(w http.ResponseWriter) {
	for key, values := range b.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := b.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(b.body.Bytes())
}
