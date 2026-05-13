package observability

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// HTTPMiddleware records HTTP request counts by method/path/status.
func HTTPMiddleware(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		Default().RecordHTTPRequest(r.Method, r.URL.Path, recorder.status)
	})
}

func SnapshotHandler(collector *Collector) http.Handler {
	if collector == nil {
		collector = Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeHandlerJSON(w, http.StatusOK, collector.Snapshot())
	})
}

func TraceHandler(collector *Collector) http.Handler {
	if collector == nil {
		collector = Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := strings.TrimSpace(r.URL.Query().Get("correlation_id"))
		if correlationID == "" {
			correlationID = strings.TrimSpace(r.URL.Query().Get("correlationId"))
		}
		if correlationID == "" {
			writeHandlerJSON(w, http.StatusBadRequest, map[string]any{
				"error": "correlation_id is required",
			})
			return
		}
		limit := 0
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 {
				writeHandlerJSON(w, http.StatusBadRequest, map[string]any{
					"error": "limit must be a non-negative integer",
				})
				return
			}
			limit = parsed
		}
		writeHandlerJSON(w, http.StatusOK, map[string]any{
			"correlation_id": correlationID,
			"events":         collector.Trace(correlationID, limit),
		})
	})
}

func writeHandlerJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (r *statusRecorder) Push(target string, opts *http.PushOptions) error {
	pusher, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (r *statusRecorder) ReadFrom(src io.Reader) (int64, error) {
	readerFrom, ok := r.ResponseWriter.(io.ReaderFrom)
	if ok {
		return readerFrom.ReadFrom(src)
	}
	return io.Copy(r.ResponseWriter, src)
}
