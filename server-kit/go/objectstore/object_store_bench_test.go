package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
)

func BenchmarkMemoryStorePutStream(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprintf("%dKB", size>>10), func(b *testing.B) {
			ctx := context.Background()
			store := benchmarkMemoryStore()
			payload := benchmarkPayload(size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key := fmt.Sprintf("stream/%d", i%16)
				if _, err := store.PutStream(ctx, key, bytes.NewReader(payload), int64(len(payload)), PutOptions{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMemoryStorePutBytes(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprintf("%dKB", size>>10), func(b *testing.B) {
			ctx := context.Background()
			store := benchmarkMemoryStore()
			payload := benchmarkPayload(size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key := fmt.Sprintf("bytes/%d", i%16)
				if _, err := store.PutBytes(ctx, key, payload, PutOptions{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMemoryStoreGetRange(b *testing.B) {
	ctx := context.Background()
	store := benchmarkMemoryStore()
	payload := benchmarkPayload(8 << 20)
	if _, err := store.PutStream(ctx, "object.bin", bytes.NewReader(payload), int64(len(payload)), PutOptions{}); err != nil {
		b.Fatal(err)
	}
	for _, size := range []int64{64 << 10, 1 << 20} {
		b.Run(fmt.Sprintf("%dKB", size>>10), func(b *testing.B) {
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				offset := int64(i) % int64(len(payload)-int(size))
				reader, _, err := store.GetRange(ctx, "object.bin", offset, size)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.Copy(io.Discard, reader); err != nil {
					_ = reader.Close()
					b.Fatal(err)
				}
				_ = reader.Close()
			}
		})
	}
}

func BenchmarkStreamingAdapterPutStreamNoSourceRetention(b *testing.B) {
	for _, size := range []int64{64 << 10, 1 << 20, 4 << 20} {
		b.Run(fmt.Sprintf("%dKB", size>>10), func(b *testing.B) {
			ctx := context.Background()
			store := streamingDiscardStore{}
			buffer := make([]byte, 64<<10)
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				reader := &deterministicReader{remaining: size}
				if _, err := store.PutStream(ctx, fmt.Sprintf("streaming/%d", i), reader, size, PutOptions{}, buffer); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkMemoryStore() *Store {
	return New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://bench",
		Bucket:   "bench",
	})
}

type streamingDiscardStore struct{}

func (streamingDiscardStore) PutStream(_ context.Context, key string, reader io.Reader, size int64, opts PutOptions, buffer []byte) (Object, error) {
	if key == "" {
		return Object{}, fmt.Errorf("object key is required")
	}
	if reader == nil {
		return Object{}, fmt.Errorf("object reader is required")
	}
	written, err := io.CopyBuffer(io.Discard, reader, buffer)
	if err != nil {
		return Object{}, err
	}
	if size >= 0 && written != size {
		return Object{}, fmt.Errorf("streaming size mismatch: got %d want %d", written, size)
	}
	return Object{Key: key, Bucket: "streaming", ContentType: firstNonEmpty(opts.ContentType, "application/octet-stream"), Size: written}, nil
}

type deterministicReader struct {
	remaining int64
	offset    int64
}

func (r *deterministicReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	for i := range p {
		p[i] = byte(((r.offset+int64(i))*31 + 7) % 251)
	}
	r.remaining -= int64(len(p))
	r.offset += int64(len(p))
	return len(p), nil
}

func benchmarkPayload(size int) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte((i*31 + i/7) % 251)
	}
	return payload
}
