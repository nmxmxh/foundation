package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// readRequestBody replaces io.ReadAll with a Content-Length-sized first buffer.
// io.ReadAll is heavily exercised stdlib; this loop is not, so it is tested
// against the cases where a hand-rolled read loop goes wrong: a declared length
// that lies in either direction, a reader that returns short reads, and a
// reader that fails partway.

// chunkReader returns at most chunk bytes per Read, which is what a real
// network body does and what a bytes.Reader never does.
type chunkReader struct {
	data  []byte
	chunk int
	err   error // returned once data is exhausted, instead of io.EOF
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		return 0, io.EOF
	}
	n := min(min(len(p), r.chunk), len(r.data))
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func (r *chunkReader) Close() error { return nil }

func requestWithBody(body io.ReadCloser, contentLength int64) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/null", nil)
	req.Body = body
	req.ContentLength = contentLength
	return req
}

func TestReadRequestBodyMatchesContentLength(t *testing.T) {
	payload := []byte(`{"user_id":"u-1","amount":42}`)
	req := requestWithBody(io.NopCloser(bytes.NewReader(payload)), int64(len(payload)))

	got, err := readRequestBody(req)
	if err != nil {
		t.Fatalf("readRequestBody: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("body = %q, want %q", got, payload)
	}
}

func TestReadRequestBodyShortReads(t *testing.T) {
	// A body delivered one byte at a time must still be assembled whole; the
	// loop must not mistake a short read for the end of the body.
	payload := []byte(`{"a":1,"b":2,"c":3}`)
	req := requestWithBody(&chunkReader{data: payload, chunk: 1}, int64(len(payload)))

	got, err := readRequestBody(req)
	if err != nil {
		t.Fatalf("readRequestBody: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("body = %q, want %q", got, payload)
	}
}

func TestReadRequestBodyLongerThanContentLength(t *testing.T) {
	// The declared length is the sizing hint, never the truncation point.
	// Truncating here would silently corrupt a body instead of letting
	// validation downstream reject it.
	payload := []byte(`{"padding":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	req := requestWithBody(io.NopCloser(bytes.NewReader(payload)), 4)

	got, err := readRequestBody(req)
	if err != nil {
		t.Fatalf("readRequestBody: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("body = %q, want %q (declared length must not truncate)", got, payload)
	}
}

func TestReadRequestBodyShorterThanContentLength(t *testing.T) {
	payload := []byte(`{}`)
	req := requestWithBody(io.NopCloser(bytes.NewReader(payload)), 4096)

	got, err := readRequestBody(req)
	if err != nil {
		t.Fatalf("readRequestBody: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("body = %q, want %q", got, payload)
	}
}

func TestReadRequestBodyUnknownAndZeroLength(t *testing.T) {
	payload := []byte(`{"chunked":true}`)

	t.Run("unknown length falls back to ReadAll", func(t *testing.T) {
		req := requestWithBody(io.NopCloser(bytes.NewReader(payload)), -1)
		got, err := readRequestBody(req)
		if err != nil {
			t.Fatalf("readRequestBody: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("body = %q, want %q", got, payload)
		}
	})

	t.Run("zero length reads nothing", func(t *testing.T) {
		req := requestWithBody(io.NopCloser(bytes.NewReader(nil)), 0)
		got, err := readRequestBody(req)
		if err != nil {
			t.Fatalf("readRequestBody: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("body = %q, want empty", got)
		}
	})
}

func TestReadRequestBodyOversizedLengthFallsBack(t *testing.T) {
	// A hostile or mistaken Content-Length must not become an allocation
	// primitive: past maxPresizedBody the reader stops trusting the hint.
	payload := []byte(`{"small":true}`)
	req := requestWithBody(io.NopCloser(bytes.NewReader(payload)), maxPresizedBody+1)

	got, err := readRequestBody(req)
	if err != nil {
		t.Fatalf("readRequestBody: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("body = %q, want %q", got, payload)
	}
}

func TestReadRequestBodyPropagatesError(t *testing.T) {
	sentinel := errors.New("connection reset")
	req := requestWithBody(&chunkReader{data: []byte(`{"partial":`), chunk: 4, err: sentinel}, 64)

	_, err := readRequestBody(req)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want %v", err, sentinel)
	}
}

func TestReadRequestBodyDoesNotOverAllocate(t *testing.T) {
	// The point of the whole change: a small declared body must not pay for
	// io.ReadAll's fixed 512-byte starting buffer.
	payload := []byte(`{}`)
	reader := bytes.NewReader(payload)
	req := requestWithBody(io.NopCloser(reader), int64(len(payload)))

	sized := testing.AllocsPerRun(100, func() {
		reader.Reset(payload)
		if _, err := readRequestBody(req); err != nil {
			t.Fatal(err)
		}
	})
	// One allocation: the exact-sized buffer. io.ReadAll costs the same count
	// but starts at 512 bytes, so the win is bytes, not allocations.
	if sized > 1 {
		t.Fatalf("readRequestBody took %.0f allocations for a 2-byte body, want <= 1", sized)
	}

	unsized := testing.AllocsPerRun(100, func() {
		reader.Reset(payload)
		if _, err := io.ReadAll(req.Body); err != nil {
			t.Fatal(err)
		}
	})
	if unsized < sized {
		t.Fatalf("sized read (%.0f allocs) should not exceed io.ReadAll (%.0f allocs)", sized, unsized)
	}
}

func TestReadRequestBodyMatchesReadAllSemantics(t *testing.T) {
	// Differential check: for every shape, the sized reader must return exactly
	// what io.ReadAll would have.
	payloads := []string{"", "{}", `{"a":1}`, strings.Repeat("x", 511), strings.Repeat("y", 513)}
	for _, payload := range payloads {
		for _, declared := range []int64{-1, 0, 1, int64(len(payload)), int64(len(payload)) + 8} {
			want, err := io.ReadAll(strings.NewReader(payload))
			if err != nil {
				t.Fatalf("ReadAll baseline: %v", err)
			}
			req := requestWithBody(io.NopCloser(strings.NewReader(payload)), declared)
			got, err := readRequestBody(req)
			if err != nil {
				t.Fatalf("readRequestBody(len=%d, declared=%d): %v", len(payload), declared, err)
			}
			// ContentLength 0 means "no body" for a server request, so the
			// reader is entitled to skip it; that is the one divergence.
			if declared == 0 {
				continue
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("len=%d declared=%d: got %d bytes, ReadAll got %d", len(payload), declared, len(got), len(want))
			}
		}
	}
}
