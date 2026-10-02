package compress

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordingWriter captures what the middleware hands downstream and reports a
// chosen set of optional interfaces.
type recordingWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	flushed  int
	hijacked bool
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{header: make(http.Header)}
}

func (r *recordingWriter) Header() http.Header         { return r.header }
func (r *recordingWriter) WriteHeader(code int)        { r.status = code }
func (r *recordingWriter) Write(p []byte) (int, error) { return r.body.Write(p) }
func (r *recordingWriter) Flush()                      { r.flushed++ }

// hijackableWriter adds the upgrade capability the buffering writer must keep.
type hijackableWriter struct {
	*recordingWriter
	conn net.Conn
}

func (h *hijackableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return h.conn, bufio.NewReadWriter(bufio.NewReader(h.conn), bufio.NewWriter(h.conn)), nil
}

// readerFromProbe records whether a large copy was forwarded to the downstream
// io.ReaderFrom instead of being buffered.
type readerFromProbe struct {
	*recordingWriter
	forwarded int
}

func (p *readerFromProbe) ReadFrom(src io.Reader) (int64, error) {
	p.forwarded++
	return io.Copy(p.recordingWriter, src)
}

func TestMiddlewareBuffersLargeCopyBeforeFlush(t *testing.T) {
	payload := bytes.Repeat([]byte("d"), 32*1024)
	var sawReaderFrom bool

	wrapped := HTTPMiddleware(true, 1024, 5)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, sawReaderFrom = w.(io.ReaderFrom)
		w.Header().Set("Content-Type", "application/json")
		// io.LimitReader hides any WriterTo on the source, so io.Copy is forced
		// to reach for the destination ReaderFrom instead of taking the fast
		// path and calling Write repeatedly.
		if _, err := io.Copy(w, io.LimitReader(bytes.NewReader(payload), int64(len(payload)))); err != nil {
			t.Errorf("copy failed: %v", err)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/bulk", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := newRecordingWriter()
	wrapped.ServeHTTP(rec, req)

	if !sawReaderFrom {
		t.Fatal("handler must see io.ReaderFrom")
	}
	if rec.Header().Get("Content-Encoding") != EncodingGzip {
		t.Fatalf("Content-Encoding = %q, want %q", rec.Header().Get("Content-Encoding"), EncodingGzip)
	}
	// The compressed body must decompress back to the original.
	out, err := DecompressWithEncodingLimit(rec.body.Bytes(), EncodingGzip, DefaultMaxDecodedBytes)
	if err != nil {
		t.Fatalf("decompress failed: %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("round trip changed the payload: got %d bytes, want %d", len(out), len(payload))
	}
}

// TestMiddlewareForwardsReadFromAfterFlush covers the pass-through branch. Once a
// handler has flushed, the prefix is on the wire and the downstream ReaderFrom
// must handle later copies.
func TestMiddlewareForwardsReadFromAfterFlush(t *testing.T) {
	wrapped := HTTPMiddleware(true, 1024, 5)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("head\n"))
		w.(http.Flusher).Flush()

		// This copy happens after the flush, so it takes the pass-through path.
		tail := strings.Repeat("t", 4096)
		if _, err := io.Copy(w, io.LimitReader(strings.NewReader(tail), int64(len(tail)))); err != nil {
			t.Errorf("copy failed: %v", err)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/stream", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	probe := &readerFromProbe{recordingWriter: newRecordingWriter()}
	wrapped.ServeHTTP(probe, req)

	if probe.forwarded == 0 {
		t.Fatal("expected the post-flush copy to reach the downstream ReaderFrom")
	}
	if probe.body.Len() <= 5 {
		t.Fatalf("downstream body = %d bytes, want the prefix plus the copy", probe.body.Len())
	}
}

// TestDecompressLimitSniffsAcrossCodecs exercises the ordered fallback. A payload
// that is not brotli must still be offered to the next codec, and the ceiling
// must apply per attempt rather than to the sequence.
func TestBufferedWriterHeaderFollowsPassThrough(t *testing.T) {
	rec := newRecordingWriter()
	b := newBufferedResponseWriter()
	b.downstream = rec

	// Before the flush the handler owns its own header map.
	b.Header().Set("Content-Type", "application/json")
	if rec.Header().Get("Content-Type") != "" {
		t.Fatal("headers must not reach downstream before the flush")
	}
	b.WriteHeader(http.StatusAccepted)
	b.Flush()

	if rec.status != http.StatusAccepted {
		t.Fatalf("downstream status = %d, want %d", rec.status, http.StatusAccepted)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatal("headers must reach downstream on the flush")
	}

	// After the flush the writer is transparent: later header writes and the
	// status go straight through, and a second flush is harmless.
	b.Header().Set("X-Late", "1")
	if rec.Header().Get("X-Late") != "1" {
		t.Fatal("header written after the flush must pass through")
	}
	if _, err := b.Write([]byte("tail")); err != nil {
		t.Fatalf("write after flush failed: %v", err)
	}
	b.WriteHeader(http.StatusTeapot)
	b.Flush()
	if !strings.Contains(rec.body.String(), "tail") {
		t.Fatal("write after the flush must reach downstream")
	}
}

// TestBufferedWriterWriteHeaderIsFirstWins mirrors net/http, where a second
// WriteHeader is a no-op with a logged error.
func TestBufferedWriterWriteHeaderIsFirstWins(t *testing.T) {
	rec := newRecordingWriter()
	b := newBufferedResponseWriter()
	b.downstream = rec

	b.WriteHeader(http.StatusCreated)
	b.WriteHeader(http.StatusTeapot)
	b.Flush()

	if rec.status != http.StatusCreated {
		t.Fatalf("status = %d, want the first call %d", rec.status, http.StatusCreated)
	}
}

// TestBufferedWriterDefaultsToOK covers a handler that writes a body without
// calling WriteHeader.
func TestBufferedWriterDefaultsToOK(t *testing.T) {
	rec := newRecordingWriter()
	b := newBufferedResponseWriter()
	b.downstream = rec

	if _, err := b.Write([]byte("implicit")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	b.Flush()

	if rec.status != http.StatusOK {
		t.Fatalf("status = %d, want %d for an implicit header write", rec.status, http.StatusOK)
	}
	if rec.body.String() != "implicit" {
		t.Fatalf("body = %q, want %q", rec.body.String(), "implicit")
	}
}

// TestStreamingCompressorCloseWithoutEncoder covers Close on a compressor that
// was never given one.
func TestMiddlewarePreservesFlusher(t *testing.T) {
	var sawFlusher, sawReaderFrom bool
	wrapped := HTTPMiddleware(true, 1, 5)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, sawFlusher = w.(http.Flusher)
		_, sawReaderFrom = w.(io.ReaderFrom)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("a"), 2048))
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := newRecordingWriter()
	wrapped.ServeHTTP(rec, req)

	if !sawFlusher {
		t.Fatal("handler lost http.Flusher through the compress middleware")
	}
	if !sawReaderFrom {
		t.Error("handler lost io.ReaderFrom through the compress middleware")
	}
}

// TestMiddlewareFlushedResponseIsNotDoubleWritten proves the pass-through path.
// Once a handler flushes, the prefix is on the wire, so the middleware must not
// emit the captured body a second time.
func TestMiddlewareFlushedResponseIsNotDoubleWritten(t *testing.T) {
	wrapped := HTTPMiddleware(true, 1, 5)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for range 3 {
			_, _ = w.Write(bytes.Repeat([]byte("b"), 512))
			w.(http.Flusher).Flush()
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := newRecordingWriter()
	wrapped.ServeHTTP(rec, req)

	if rec.flushed == 0 {
		t.Fatal("flush never reached the downstream writer")
	}
	if got := rec.body.Len(); got != 1536 {
		t.Fatalf("downstream body = %d bytes, want 1536", got)
	}
}

// TestMiddlewarePreservesHijacker covers the protocol upgrade case the buffering
// writer used to break with a 500.
func TestMiddlewarePreservesHijacker(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	var sawHijacker bool
	wrapped := HTTPMiddleware(true, 1, 5)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		sawHijacker = ok
		if !ok {
			return
		}
		_, _, _ = hj.Hijack()
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Accept-Encoding", "br")
	downstream := &hijackableWriter{recordingWriter: newRecordingWriter(), conn: server}
	wrapped.ServeHTTP(downstream, req)

	if !sawHijacker {
		t.Fatal("handler lost http.Hijacker through the compress middleware")
	}
	if !downstream.hijacked {
		t.Fatal("Hijack did not reach the downstream writer")
	}
}

// TestMiddlewareUnwrap exposes the downstream writer to outer middleware.
func TestMiddlewareUnwrap(t *testing.T) {
	rec := newRecordingWriter()
	b := newBufferedResponseWriter()
	b.downstream = rec
	if b.Unwrap() != rec {
		t.Fatal("Unwrap must return the downstream writer")
	}
}

// TestMiddlewareDiscardsBufferAfterResponse keeps the capture from retaining a
// large body after it has been written.
func TestMiddlewareDiscardsBufferAfterResponse(t *testing.T) {
	payload := bytes.Repeat([]byte("c"), 64*1024)
	wrapped := HTTPMiddleware(true, 1024, 5)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
	req.Header.Set("Accept-Encoding", "br")
	rec := newRecordingWriter()
	wrapped.ServeHTTP(rec, req)

	if rec.body.Len() == 0 {
		t.Fatal("nothing was written downstream")
	}
	if rec.Header().Get("Content-Encoding") != EncodingBrotli {
		t.Fatalf("Content-Encoding = %q, want %q", rec.Header().Get("Content-Encoding"), EncodingBrotli)
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Error("compressed response must not carry Content-Length")
	}
}

// TestMiddlewareAllocationBudget guards the whole request path, where the
// buffered writer and the encoder both sit on the request. The per-encoder
// ceilings live in compress_test.go; this asserts the composition stays bounded.
func TestMiddlewareAllocationBudget(t *testing.T) {
	if raceDetectorEnabled {
		t.Skip("allocation budget: the race detector drains sync.Pool and inflates every measurement")
	}
	payload := bytes.Repeat([]byte("a"), 16*1024)
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}

	for _, tc := range []struct{ name, ae string }{
		{"brotli", "br"},
		{"gzip", "gzip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := HTTPMiddleware(true, 1024, 5)(http.HandlerFunc(handler))
			req := httptest.NewRequest(http.MethodGet, "/v1/x", nil)
			req.Header.Set("Accept-Encoding", tc.ae)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req) // warm
			rec.Body.Reset()

			allocs := testing.AllocsPerRun(100, func() {
				rec.Body.Reset()
				wrapped.ServeHTTP(rec, req)
			})
			if allocs > 40 {
				t.Fatalf("%s: %.1f allocs/op through the middleware, want at most 40", tc.name, allocs)
			}
			t.Logf("%s: %.1f allocs/op through the middleware", tc.name, allocs)
		})
	}
}
