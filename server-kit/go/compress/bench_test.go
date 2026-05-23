package compress

import (
	"testing"
)

func BenchmarkCompressLargeBatch(b *testing.B) {
	// Create a 1MB payload of semi-random data to simulate a large batch envelope
	// (Some repetition to allow compression to actually work)
	data := compressionFixture()

	b.Run("Brotli-Q4", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = CompressBrotli(data, 4)
		}
	})

	b.Run("Zstd-Fastest", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = CompressZstd(data)
		}
	})
}

func BenchmarkDecompressLargeBatch(b *testing.B) {
	data := compressionFixture()

	brData, _ := CompressBrotli(data, 4)
	zstdData, _ := CompressZstd(data)

	b.Run("Brotli", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = decompressBrotli(brData)
		}
	})

	b.Run("Zstd", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = decompressZstd(zstdData)
		}
	})
}

func TestCompressionRatio(t *testing.T) {
	data := compressionFixture()

	brData, err := CompressBrotli(data, 4)
	if err != nil {
		t.Fatalf("brotli compress failed: %v", err)
	}
	zstdData, err := CompressZstd(data)
	if err != nil {
		t.Fatalf("zstd compress failed: %v", err)
	}

	if len(brData) >= len(data) {
		t.Fatalf("brotli did not reduce deterministic fixture: original=%d compressed=%d", len(data), len(brData))
	}
	if len(zstdData) >= len(data) {
		t.Fatalf("zstd did not reduce deterministic fixture: original=%d compressed=%d", len(data), len(zstdData))
	}
}

func compressionFixture() []byte {
	data := make([]byte, 1024*1024)
	for i := range data[:512*1024] {
		data[i] = byte((i*31 + i/7) % 251)
	}
	copy(data[512*1024:], data[:512*1024])
	return data
}
