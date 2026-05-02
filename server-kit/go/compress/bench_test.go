package compress

import (
	"crypto/rand"
	"fmt"
	"testing"
)

func BenchmarkCompressLargeBatch(b *testing.B) {
	// Create a 1MB payload of semi-random data to simulate a large batch envelope
	// (Some repetition to allow compression to actually work)
	data := make([]byte, 1024*1024)
	_, _ = rand.Read(data[:512*1024])
	copy(data[512*1024:], data[:512*1024])

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
	data := make([]byte, 1024*1024)
	_, _ = rand.Read(data[:512*1024])
	copy(data[512*1024:], data[:512*1024])

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
	// Create a 1MB payload of semi-random data
	data := make([]byte, 1024*1024)
	_, _ = rand.Read(data[:512*1024])
	copy(data[512*1024:], data[:512*1024])

	brData, _ := CompressBrotli(data, 4)
	zstdData, _ := CompressZstd(data)

	fmt.Printf("Original Size: %d KB\n", len(data)/1024)
	fmt.Printf("Brotli Size:   %d KB (Ratio: %.2f)\n", len(brData)/1024, float64(len(data))/float64(len(brData)))
	fmt.Printf("Zstd Size:     %d KB (Ratio: %.2f)\n", len(zstdData)/1024, float64(len(data))/float64(len(zstdData)))
}
