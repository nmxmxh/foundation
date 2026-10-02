package compress

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
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
			// Cheap header/path skips run before PreferredEncoding: parsing the
			// Accept-Encoding qvalues allocates a map, and a WebSocket handshake
			// (the common projection case) would only discard it. Order matters
			// for allocations, not correctness — all three lead to the same skip.
			if isUpgradeRequest(r) || shouldSkipCompressionPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			// Resolve the encoding once. PreferredEncoding allocates a map, and
			// Compress would otherwise parse the same header again.
			encoding := PreferredEncoding(r.Header.Get("Accept-Encoding"))
			if encoding == "" {
				next.ServeHTTP(w, r)
				return
			}

			capture := newBufferedResponseWriter()
			capture.downstream = w
			next.ServeHTTP(capture, r)

			// A handler that flushed already put its prefix on the wire, so the
			// response is finished. Compressing it now would emit a second body.
			if capture.passthrough {
				return
			}

			payload := capture.body.Bytes()
			if len(payload) < minBytes || !isCompressibleContentType(capture.header.Get("Content-Type")) || capture.header.Get("Content-Encoding") != "" {
				capture.flushTo(w)
				capture.reset()
				return
			}

			compressed, usedEncoding, err := CompressWithEncoding(payload, encoding, level)
			if err != nil || usedEncoding == "" || usedEncoding == EncodingIdentity || len(compressed) >= len(payload) {
				capture.flushTo(w)
				capture.reset()
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
			capture.reset()
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
			payload, err := readAllLimited(r.Body, maxDecodedBytes+1, r.ContentLength)
			_ = r.Body.Close()
			if err != nil {
				http.Error(w, "invalid request body", http.StatusBadRequest)
				return
			}
			// Bounded during the decode, not after it: the previous form
			// measured decoded once it was already resident, which a bomb
			// reaching gigabytes never survives to answer.
			decoded, err := DecompressWithEncodingLimit(payload, encoding, maxDecodedBytes)
			if err != nil {
				writeDecodeError(w, err)
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

// readAllLimited reads at most limit bytes, sizing the buffer from the declared
// content length when the client supplied one. The bound is the important part;
// the sizing only removes the doubling churn that io.ReadAll pays on a body of
// known length.
func readAllLimited(body io.Reader, limit int64, contentLength int64) ([]byte, error) {
	buf := make([]byte, 0, 512)
	if contentLength > 0 && contentLength <= limit {
		buf = make([]byte, 0, contentLength)
	}
	return readAllInto(buf, io.LimitReader(body, limit))
}

// writeDecodeError separates "too big" from "malformed": both are refusals,
// but only one tells the client that a smaller body would have worked.
func writeDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrDecodedTooLarge) {
		http.Error(w, "request body too large after decompression", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "invalid compressed request body", http.StatusBadRequest)
}

// isUpgradeRequest reports whether the request negotiates a protocol upgrade
// (WebSocket and friends). Upgraded connections are hijacked from the HTTP
// layer; the buffering writer implements no http.Hijacker, so wrapping such a
// request breaks the handshake with a 500 (observed on projection WebSockets
// whenever a proxy adds Accept-Encoding). The skip must be protocol-based —
// upgrades can arrive on any path, not just /ws.
func isUpgradeRequest(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("Upgrade")) != ""
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

// bufferedResponseWriter captures a response so it can be compressed as one
// unit. It stays transparent to the optional interfaces of the writer it wraps:
// a handler that flushes, hijacks, or writes through io.ReaderFrom must keep
// working, and a handler that flushes has opted out of buffering.
//
// A flush mid-response switches the writer into pass-through mode. The buffered
// prefix is emitted, and later writes go straight to the downstream writer
// without compression, because the head is already on the wire. This keeps
// time-to-first-byte honest for streamed responses.
type bufferedResponseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
	// downstream is set once the response is in pass-through mode.
	downstream http.ResponseWriter
	// passthrough reports that the buffered prefix has already been emitted.
	passthrough bool
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{
		header: make(http.Header, 8),
	}
}

func (b *bufferedResponseWriter) Header() http.Header {
	if b.passthrough {
		return b.downstream.Header()
	}
	return b.header
}

func (b *bufferedResponseWriter) Write(data []byte) (int, error) {
	if b.passthrough {
		return b.downstream.Write(data)
	}
	return b.body.Write(data)
}

// ReadFrom forwards large copies to the downstream writer when the response has
// left buffering, which lets net/http use sendfile-style paths.
func (b *bufferedResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	if !b.passthrough {
		// Buffer first so the response stays compressible as one unit.
		return b.body.ReadFrom(src)
	}
	if rf, ok := b.downstream.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(b.downstream, src)
}

func (b *bufferedResponseWriter) WriteHeader(statusCode int) {
	if b.passthrough {
		b.downstream.WriteHeader(statusCode)
		return
	}
	if b.status == 0 {
		b.status = statusCode
	}
}

// Flush emits the buffered prefix and switches to pass-through mode. A handler
// that calls Flush wants bytes on the wire now, so buffering stops.
func (b *bufferedResponseWriter) Flush() {
	if b.passthrough {
		if flusher, ok := b.downstream.(http.Flusher); ok {
			flusher.Flush()
		}
		return
	}
	b.flushTo(b.downstream)
	b.passthrough = true
	b.body.Reset()
}

// Hijack passes a protocol upgrade through to the downstream writer. Without it
// a handler that upgrades after the first write would fail with a 500.
func (b *bufferedResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := b.downstream.(http.Hijacker)
	if !ok {
		return nil, nil, errNotHijackable
	}
	if !b.passthrough {
		b.flushTo(b.downstream)
		b.passthrough = true
	}
	return hijacker.Hijack()
}

// Unwrap exposes the downstream writer to middleware that needs the original.
func (b *bufferedResponseWriter) Unwrap() http.ResponseWriter {
	return b.downstream
}

var errNotHijackable = errors.New("compress: underlying ResponseWriter is not an http.Hijacker")

// reset returns the capture buffers to the pool and must run after the buffered
// body has been written out.
func (b *bufferedResponseWriter) reset() {
	b.header = nil
	b.body.Reset()
	b.status = 0
	b.downstream = nil
	b.passthrough = false
}

func (b *bufferedResponseWriter) flushTo(w http.ResponseWriter) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	// Assign the whole header map in one step instead of copying key by key.
	dst := w.Header()
	for key := range dst {
		dst.Del(key)
	}
	for key, values := range b.header {
		dst[key] = values
	}
	w.WriteHeader(b.status)
	if b.body.Len() > 0 {
		_, _ = w.Write(b.body.Bytes())
	}
}

var (
	_ http.ResponseWriter = (*bufferedResponseWriter)(nil)
	_ http.Flusher        = (*bufferedResponseWriter)(nil)
	_ http.Hijacker       = (*bufferedResponseWriter)(nil)
	_ io.ReaderFrom       = (*bufferedResponseWriter)(nil)
)
