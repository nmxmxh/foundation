package compress

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"

	"github.com/andybalholm/brotli"
)

// StreamingCompressor wraps an http.ResponseWriter and compresses data on the fly.
type StreamingCompressor struct {
	w           http.ResponseWriter
	compressor  io.WriteCloser
	encoding    string
	wroteHeader bool
}

// NewStreamingCompressor creates a new StreamingCompressor based on Accept-Encoding.
func NewStreamingCompressor(w http.ResponseWriter, r *http.Request, level int) (*StreamingCompressor, bool) {
	acceptEncoding := r.Header.Get("Accept-Encoding")
	encoding := PreferredEncoding(acceptEncoding)
	if encoding == "" || encoding == EncodingIdentity {
		return nil, false
	}

	var compressor io.WriteCloser
	var err error

	switch encoding {
	case EncodingBrotli:
		compressor = brotli.NewWriterLevel(w, normalizeBrotliLevel(level))
	case EncodingGzip:
		compressor, err = gzip.NewWriterLevel(w, normalizeGzipLevel(level))
	case EncodingDeflate:
		compressor, err = flate.NewWriter(w, normalizeFlateLevel(level))
	}

	if err != nil || compressor == nil {
		return nil, false
	}

	return &StreamingCompressor{
		w:          w,
		compressor: compressor,
		encoding:   encoding,
	}, true
}

func (s *StreamingCompressor) Header() http.Header {
	return s.w.Header()
}

func (s *StreamingCompressor) Write(data []byte) (int, error) {
	if !s.wroteHeader {
		s.w.Header().Set("Content-Encoding", s.encoding)
		s.w.Header().Del("Content-Length")
		s.wroteHeader = true
	}
	return s.compressor.Write(data)
}

func (s *StreamingCompressor) WriteHeader(statusCode int) {
	if !s.wroteHeader {
		s.w.Header().Set("Content-Encoding", s.encoding)
		s.w.Header().Del("Content-Length")
		s.wroteHeader = true
	}
	s.w.WriteHeader(statusCode)
}

func (s *StreamingCompressor) Close() error {
	if s.compressor != nil {
		return s.compressor.Close()
	}
	return nil
}

func (s *StreamingCompressor) Flush() {
	if flusher, ok := s.compressor.(interface{ Flush() error }); ok {
		_ = flusher.Flush()
	} else if flusher, ok := s.compressor.(*gzip.Writer); ok {
		_ = flusher.Flush()
	}
	if flusher, ok := s.w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Ensure StreamingCompressor implements http.Flusher and http.ResponseWriter
var _ http.ResponseWriter = (*StreamingCompressor)(nil)
var _ http.Flusher = (*StreamingCompressor)(nil)
var _ io.Closer = (*StreamingCompressor)(nil)
