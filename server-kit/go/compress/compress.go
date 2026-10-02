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
	"sync"

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

// Encoder pools keep per-codec state off the request path. A brotli writer at
// quality 5 carries roughly 1 MB of internal tables; allocating one per request
// made a 1 KB response cost more than a megabyte of garbage. gzip and flate
// carry smaller state but the same per-call cost.
//
// Writers are keyed by level because the codec rejects a level change after
// construction. Reset returns the writer to a clean state and rebinds its sink.

// writerPools holds one pool per codec. Each pool stores a *sync.Pool keyed by
// compression level, because a codec cannot change level after construction.
type writerPools struct {
	byLevel sync.Map // int -> *sync.Pool
}

func (p *writerPools) pool(level int, newFn func(io.Writer) any) *sync.Pool {
	if v, ok := p.byLevel.Load(level); ok {
		return v.(*sync.Pool)
	}
	pool := &sync.Pool{New: func() any { return newFn(nil) }}
	actual, _ := p.byLevel.LoadOrStore(level, pool)
	return actual.(*sync.Pool)
}

// put returns a writer to the pool for the level it was created with. Callers
// must Reset the writer to io.Discard first so it holds no reference to the
// destination buffer.
//
// put never creates a pool. The first call to pool fixes the level's constructor,
// so a pool created here would carry a constructor that cannot build a writer,
// and every later acquire at that level would fail. Dropping the writer is the
// correct outcome: the next request builds a fresh one, which is exactly what
// the pre-pool code always did.
func put[T any](p *writerPools, level int, w T) {
	if v, ok := p.byLevel.Load(level); ok {
		v.(*sync.Pool).Put(w)
	}
}

var (
	gzipPools   writerPools
	flatePools  writerPools
	brotliPools writerPools
)

// The three getters below take an already-normalized level. Normalizing once in
// the caller is deliberate: the level keys the pool on the way in and on the way
// out, so normalizing in both places would be correct only while both call sites
// happened to agree. Holding one normalized value makes a future change to a
// normalizer unable to split the two.
//
// A pooled writer is reused for the same level only. A codec cannot change level
// after construction, so a writer fetched at level 5 must never be handed to a
// level 9 request.

// newFlateWriter and newBrotliWriter adapt constructors whose signatures do not
// fit the pool's func(io.Writer) any shape. They return a nil interface on
// failure so the caller reports the refusal instead of storing a typed nil.

func newFlateWriter(w io.Writer, level int) any {
	zw, err := flate.NewWriter(w, level)
	if err != nil {
		return nil
	}
	return zw
}

func newBrotliWriter(w io.Writer, level int) any {
	return brotli.NewWriterLevel(w, level)
}

func gzipWriter(dst io.Writer, level int) (*gzip.Writer, error) {
	pool := gzipPools.pool(level, func(w io.Writer) any {
		zw, err := gzip.NewWriterLevel(w, level)
		if err != nil {
			return nil
		}
		return zw
	})
	zw, ok := pool.Get().(*gzip.Writer)
	if !ok || zw == nil {
		// The pooled constructor refused this level. Report it rather than
		// storing a typed nil and panicking on the next Reset.
		return nil, fmt.Errorf("compress: gzip level %d rejected", level)
	}
	zw.Reset(dst)
	return zw, nil
}

func flateWriter(dst io.Writer, level int) (*flate.Writer, error) {
	pool := flatePools.pool(level, func(w io.Writer) any { return newFlateWriter(w, level) })
	zw, ok := pool.Get().(*flate.Writer)
	if !ok || zw == nil {
		return nil, fmt.Errorf("compress: flate level %d rejected", level)
	}
	zw.Reset(dst)
	return zw, nil
}

func brotliWriter(dst io.Writer, level int) *brotli.Writer {
	pool := brotliPools.pool(level, func(w io.Writer) any { return newBrotliWriter(w, level) })
	zw, ok := pool.Get().(*brotli.Writer)
	if !ok || zw == nil {
		return nil
	}
	zw.Reset(dst)
	return zw
}

// The Compress* functions below normalize the level exactly once and then pass
// that single value to both the pool fetch and the pool return. Each releases
// the writer back to the pool only after Close and after detaching it from the
// destination buffer, so a pooled writer never retains a reference to a finished
// request body.

func releaseGzip(level int, zw *gzip.Writer) {
	zw.Reset(io.Discard)
	put(&gzipPools, level, zw)
}

func releaseFlate(level int, zw *flate.Writer) {
	zw.Reset(io.Discard)
	put(&flatePools, level, zw)
}

func releaseBrotli(level int, zw *brotli.Writer) {
	zw.Reset(io.Discard)
	put(&brotliPools, level, zw)
}

// CompressGzip compresses data with gzip at the configured level.
func CompressGzip(data []byte, level int) ([]byte, error) {
	level = normalizeGzipLevel(level)
	var buf bytes.Buffer
	buf.Grow(len(data)/2 + 64)
	zw, err := gzipWriter(&buf, level)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	releaseGzip(level, zw)
	return buf.Bytes(), nil
}

// CompressBrotli compresses data with brotli at a balanced quality level.
func CompressBrotli(data []byte, level int) ([]byte, error) {
	level = normalizeBrotliLevel(level)
	var buf bytes.Buffer
	buf.Grow(len(data)/2 + 64)
	zw := brotliWriter(&buf, level)
	if zw == nil {
		return nil, fmt.Errorf("compress: brotli level %d rejected", level)
	}
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	releaseBrotli(level, zw)
	return buf.Bytes(), nil
}

// CompressFlate compresses data with flate at the configured level.
func CompressFlate(data []byte, level int) ([]byte, error) {
	level = normalizeFlateLevel(level)
	var buf bytes.Buffer
	buf.Grow(len(data)/2 + 64)
	zw, err := flateWriter(&buf, level)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	releaseFlate(level, zw)
	return buf.Bytes(), nil
}

// Compress negotiates the most suitable transport encoding.
// Compress encodes data with the first encoding the client accepts.
func Compress(data []byte, acceptEncoding string, level int) ([]byte, string, error) {
	return CompressWithEncoding(data, PreferredEncoding(acceptEncoding), level)
}

// CompressWithEncoding is Compress for a caller that already resolved the
// encoding. It exists because the middleware must ask whether compression is
// wanted before it buffers, then reuse that answer, and parsing Accept-Encoding
// twice costs a map allocation per request.
func CompressWithEncoding(data []byte, encoding string, level int) ([]byte, string, error) {
	switch encoding {
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
// readLimited drains a decompressor under a byte ceiling. The buffer starts
// from the encoded length, which is a lower bound on the decoded size for every
// codec here, so a small body stops paying io.ReadAll's doubling ladder.
func readLimited(r io.Reader, maxDecoded int64) ([]byte, error) {
	out := make([]byte, 0, 512)
	if size, ok := r.(interface{ Len() int }); ok && size.Len() > 0 {
		out = make([]byte, 0, size.Len())
	}
	out, err := readAllInto(out, io.LimitReader(r, maxDecoded+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > maxDecoded {
		return nil, ErrDecodedTooLarge
	}
	return out, nil
}

// readAllInto appends to buf instead of allocating a fresh one.
func readAllInto(buf []byte, r io.Reader) ([]byte, error) {
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf, nil
			}
			return buf, err
		}
	}
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
