package compress

import (
	"bytes"
	"compress/bzip2"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
)

const (
	EncodingIdentity = "identity"
	EncodingBrotli   = "br"
	EncodingGzip     = "gzip"
	EncodingDeflate  = "deflate"
)

// CompressGzip compresses data with gzip at the configured level.
func CompressGzip(data []byte, level int) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, normalizeGzipLevel(level))
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CompressBrotli compresses data with brotli at a balanced quality level.
func CompressBrotli(data []byte, level int) ([]byte, error) {
	var buf bytes.Buffer
	zw := brotli.NewWriterLevel(&buf, normalizeBrotliLevel(level))
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CompressFlate compresses data with flate at the configured level.
func CompressFlate(data []byte, level int) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := flate.NewWriter(&buf, normalizeFlateLevel(level))
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Compress negotiates the most suitable transport encoding.
func Compress(data []byte, acceptEncoding string, level int) ([]byte, string, error) {
	switch PreferredEncoding(acceptEncoding) {
	case EncodingBrotli:
		compressed, err := CompressBrotli(data, level)
		return compressed, EncodingBrotli, err
	case EncodingGzip:
		compressed, err := CompressGzip(data, level)
		return compressed, EncodingGzip, err
	case EncodingDeflate:
		compressed, err := CompressFlate(data, level)
		return compressed, EncodingDeflate, err
	default:
		return data, EncodingIdentity, nil
	}
}

func CompressBest(data []byte, level int) ([]byte, string, error) {
	compressed, err := CompressBrotli(data, level)
	if err == nil && len(compressed) < len(data) {
		return compressed, EncodingBrotli, nil
	}
	compressed, err = CompressGzip(data, level)
	if err == nil && len(compressed) < len(data) {
		return compressed, EncodingGzip, nil
	}
	compressed, err = CompressFlate(data, level)
	if err == nil && len(compressed) < len(data) {
		return compressed, EncodingDeflate, nil
	}
	if err != nil {
		return nil, "", err
	}
	return data, EncodingIdentity, nil
}

// Decompress attempts brotli first, then gzip, then flate.
func Decompress(data []byte) ([]byte, error) {
	if out, err := decompressBrotli(data); err == nil {
		return out, nil
	}
	if out, err := decompressGzip(data); err == nil {
		return out, nil
	}
	return decompressFlate(data)
}

func DecompressWithEncoding(data []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", EncodingIdentity:
		return append([]byte(nil), data...), nil
	case EncodingBrotli:
		return decompressBrotli(data)
	case EncodingGzip:
		return decompressGzip(data)
	case EncodingDeflate:
		return decompressFlate(data)
	case "bzip2":
		return io.ReadAll(bzip2.NewReader(bytes.NewReader(data)))
	default:
		return nil, fmt.Errorf("unsupported content encoding: %s", encoding)
	}
}

func decompressBrotli(data []byte) ([]byte, error) {
	return io.ReadAll(brotli.NewReader(bytes.NewReader(data)))
}

func decompressGzip(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	out, readErr := io.ReadAll(zr)
	closeErr := zr.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return out, nil
}

func decompressFlate(data []byte) ([]byte, error) {
	zr := flate.NewReader(bytes.NewReader(data))
	out, readErr := io.ReadAll(zr)
	closeErr := zr.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return out, nil
}

func PreferredEncoding(acceptEncoding string) string {
	qvalues := parseEncodingQValues(acceptEncoding)
	switch {
	case qvalues[EncodingBrotli] > 0:
		return EncodingBrotli
	case qvalues[EncodingGzip] > 0:
		return EncodingGzip
	case qvalues[EncodingDeflate] > 0:
		return EncodingDeflate
	case qvalues["*"] > 0:
		return EncodingBrotli
	default:
		return ""
	}
}

func CanGzip(acceptEncoding string) bool {
	return strings.Contains(strings.ToLower(acceptEncoding), "gzip")
}

func parseEncodingQValues(acceptEncoding string) map[string]float64 {
	values := map[string]float64{}
	for _, item := range strings.Split(strings.ToLower(strings.TrimSpace(acceptEncoding)), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.Split(item, ";")
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		qvalue := 1.0
		for _, part := range parts[1:] {
			part = strings.TrimSpace(part)
			if !strings.HasPrefix(part, "q=") {
				continue
			}
			if parsed, err := strconv.ParseFloat(strings.TrimPrefix(part, "q="), 64); err == nil {
				qvalue = parsed
			}
		}
		values[name] = qvalue
	}
	return values
}



func normalizeBrotliLevel(level int) int {
	switch {
	case level <= 0:
		return 5
	case level > 11:
		return 11
	default:
		return level
	}
}

func normalizeGzipLevel(level int) int {
	if level < gzip.HuffmanOnly {
		return gzip.DefaultCompression
	}
	if level > gzip.BestCompression {
		return gzip.BestSpeed
	}
	return level
}

func normalizeFlateLevel(level int) int {
	switch {
	case level < flate.HuffmanOnly:
		return flate.BestSpeed
	case level > flate.BestCompression:
		return flate.BestSpeed
	default:
		return level
	}
}
