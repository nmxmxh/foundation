package compress

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

const (
	EncodingIdentity = "identity"
	EncodingBrotli   = "br"
	EncodingZstd     = "zstd"
	EncodingGzip     = "gzip"
	EncodingDeflate  = "deflate"
)

// DefaultMaxDecodedBytes bounds a decode whose caller states no ceiling of
// its own. Compression ratios are attacker-controlled — gzip reaches roughly
// 1000:1 and zstd far beyond it — so a few megabytes of crafted input expands
// to gigabytes. Measuring the result after decoding is too late: the
// allocation has already happened. Every decode here is bounded while it runs.
const DefaultMaxDecodedBytes int64 = 64 << 20

// ErrDecodedTooLarge reports input that expands past the caller's ceiling.
// Callers serving HTTP should answer 413 rather than 400: the payload is
// well-formed, it is merely too large once decoded.
var ErrDecodedTooLarge = errors.New("compressed payload exceeds decoded size limit")

var (
	zstdEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	// DecodeAll does not stream, so the decoder's own ceiling is the only
	// bound available to it. The library default is 64 GiB — reachable by a
	// single request — so it is pinned to the package default here and
	// tightened per call by the length check in decompressZstd.
	zstdDecoder, _ = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(uint64(DefaultMaxDecodedBytes)))
)

// CompressZstd compresses data with Zstd at the configured level.
func CompressZstd(data []byte) ([]byte, error) {
	return zstdEncoder.EncodeAll(data, make([]byte, 0, len(data))), nil
}

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
	case EncodingZstd:
		compressed, err := CompressZstd(data)
		return compressed, EncodingZstd, err
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
	compressed, err = CompressZstd(data)
	if err == nil && len(compressed) < len(data) {
		return compressed, EncodingZstd, nil
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

// Decompress attempts brotli first, then zstd, then gzip, then flate, under
// DefaultMaxDecodedBytes. Use DecompressLimit to state a tighter ceiling.
func Decompress(data []byte) ([]byte, error) {
	return DecompressLimit(data, DefaultMaxDecodedBytes)
}

// DecompressLimit is Decompress bounded by maxDecoded.
//
// Sniffing tries each codec in turn, so a bomb that fails to be one format is
// still offered to the next. The bound applies to every attempt, not to the
// sequence: cost stays maxDecoded+1 however many codecs are tried.
func DecompressLimit(data []byte, maxDecoded int64) ([]byte, error) {
	maxDecoded = normalizeDecodeLimit(maxDecoded)
	if out, err := decompressBrotli(data, maxDecoded); err == nil {
		return out, nil
	} else if errors.Is(err, ErrDecodedTooLarge) {
		return nil, err
	}
	if out, err := decompressZstd(data, maxDecoded); err == nil {
		return out, nil
	} else if errors.Is(err, ErrDecodedTooLarge) {
		return nil, err
	}
	if out, err := decompressGzip(data, maxDecoded); err == nil {
		return out, nil
	} else if errors.Is(err, ErrDecodedTooLarge) {
		return nil, err
	}
	return decompressFlate(data, maxDecoded)
}

// DecompressWithEncoding decodes data under DefaultMaxDecodedBytes. Callers
// handling untrusted input should state their own ceiling with
// DecompressWithEncodingLimit.
func DecompressWithEncoding(data []byte, encoding string) ([]byte, error) {
	return DecompressWithEncodingLimit(data, encoding, DefaultMaxDecodedBytes)
}

// DecompressWithEncodingLimit decodes data, refusing to allocate beyond
// maxDecoded. The ceiling is enforced during the decode, so a decompression
// bomb costs maxDecoded+1 bytes instead of its full expanded size.
func DecompressWithEncodingLimit(data []byte, encoding string, maxDecoded int64) ([]byte, error) {
	maxDecoded = normalizeDecodeLimit(maxDecoded)
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", EncodingIdentity:
		if int64(len(data)) > maxDecoded {
			return nil, ErrDecodedTooLarge
		}
		return append([]byte(nil), data...), nil
	case EncodingBrotli:
		return decompressBrotli(data, maxDecoded)
	case EncodingZstd:
		return decompressZstd(data, maxDecoded)
	case EncodingGzip:
		return decompressGzip(data, maxDecoded)
	case EncodingDeflate:
		return decompressFlate(data, maxDecoded)
	default:
		return nil, fmt.Errorf("unsupported content encoding: %s", encoding)
	}
}

func normalizeDecodeLimit(maxDecoded int64) int64 {
	if maxDecoded <= 0 {
		return DefaultMaxDecodedBytes
	}
	return maxDecoded
}

// readLimited drains r one byte past the ceiling. Reading maxDecoded+1 is what
// distinguishes "exactly at the limit" from "over it" without trusting any
// length the payload declares about itself.
func readLimited(r io.Reader, maxDecoded int64) ([]byte, error) {
	out, err := io.ReadAll(io.LimitReader(r, maxDecoded+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > maxDecoded {
		return nil, ErrDecodedTooLarge
	}
	return out, nil
}

func decompressBrotli(data []byte, maxDecoded int64) ([]byte, error) {
	return readLimited(brotli.NewReader(bytes.NewReader(data)), maxDecoded)
}

func decompressZstd(data []byte, maxDecoded int64) ([]byte, error) {
	out, err := zstdDecoder.DecodeAll(data, nil)
	if err != nil {
		// The decoder's own ceiling is DefaultMaxDecodedBytes; report a
		// breach of it in the same terms as a breach of the caller's.
		if errors.Is(err, zstd.ErrDecoderSizeExceeded) {
			return nil, ErrDecodedTooLarge
		}
		return nil, err
	}
	if int64(len(out)) > maxDecoded {
		return nil, ErrDecodedTooLarge
	}
	return out, nil
}

func decompressGzip(data []byte, maxDecoded int64) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	out, readErr := readLimited(zr, maxDecoded)
	closeErr := zr.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return out, nil
}

func decompressFlate(data []byte, maxDecoded int64) ([]byte, error) {
	zr := flate.NewReader(bytes.NewReader(data))
	out, readErr := readLimited(zr, maxDecoded)
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
	case qvalues[EncodingZstd] > 0:
		return EncodingZstd
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
	for item := range strings.SplitSeq(strings.ToLower(strings.TrimSpace(acceptEncoding)), ",") {
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
