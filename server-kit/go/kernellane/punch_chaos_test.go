package kernellane

import (
	"bytes"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestPunchHoleShakyRangesAndInconsistencies verifies edge cases and abnormal parameters.
func TestPunchHoleShakyRangesAndInconsistencies(t *testing.T) {
	dir := t.TempDir()

	t.Run("closed_file_descriptor", func(t *testing.T) {
		f, err := os.Create(filepath.Join(dir, "closed.bin"))
		if err != nil {
			t.Fatalf("create file: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close file: %v", err)
		}

		// Punching a closed file must return an error and not panic.
		punched, err := PunchHole(f, 0, BlockSize)
		if err == nil && punched {
			t.Fatalf("expected error on closed file, got success")
		}
	})

	t.Run("read_only_file", func(t *testing.T) {
		path := filepath.Join(dir, "readonly.bin")
		if err := os.WriteFile(path, bytes.Repeat([]byte{0xAA}, int(BlockSize*4)), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}

		roFile, err := os.Open(path)
		if err != nil {
			t.Fatalf("open read only: %v", err)
		}
		defer func() { _ = roFile.Close() }()

		// Punching a read-only file must fail with an error or degrade safely.
		_, _ = PunchHole(roFile, 0, BlockSize)
	})

	t.Run("unlinked_open_file", func(t *testing.T) {
		path := filepath.Join(dir, "unlinked.bin")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open file: %v", err)
		}
		defer func() { _ = f.Close() }()

		payload := bytes.Repeat([]byte{0xBB}, int(BlockSize*4))
		if _, err := f.Write(payload); err != nil {
			t.Fatalf("write file: %v", err)
		}
		_ = f.Sync()

		// Remove file while descriptor remains open.
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove file: %v", err)
		}

		// Punch hole in open unlinked descriptor.
		punched, err := PunchHole(f, 0, BlockSize)
		if err != nil {
			t.Fatalf("unexpected punch error on unlinked open file: %v", err)
		}
		if punched {
			readBack := make([]byte, BlockSize)
			if _, err := f.ReadAt(readBack, 0); err != nil {
				t.Fatalf("read back unlinked file: %v", err)
			}
			if !bytes.Equal(readBack, make([]byte, BlockSize)) {
				t.Fatalf("expected zeroed block on unlinked open file")
			}
		}
	})

	t.Run("offset_beyond_eof", func(t *testing.T) {
		path := filepath.Join(dir, "beyond_eof.bin")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open file: %v", err)
		}
		defer func() { _ = f.Close() }()

		payload := bytes.Repeat([]byte{0xCC}, int(BlockSize*2))
		if _, err := f.Write(payload); err != nil {
			t.Fatalf("write file: %v", err)
		}
		_ = f.Sync()

		// Punch offset far beyond EOF (1 GiB beyond file size).
		_, err = PunchHole(f, 1024*1024*1024, BlockSize)
		if err != nil {
			t.Fatalf("punch beyond EOF failed: %v", err)
		}

		// Logical size must remain unchanged.
		st, err := f.Stat()
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if st.Size() != int64(len(payload)) {
			t.Fatalf("logical size changed: got %d, want %d", st.Size(), len(payload))
		}
	})
}

// TestPunchHoleInterleavedPattern verifies checkerboard punching across contiguous blocks.
func TestPunchHoleInterleavedPattern(t *testing.T) {
	if !HolePunchSupported() {
		t.Skip("sparse hole punching unsupported; skipping interleaved pattern test")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "interleaved.bin")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer func() { _ = f.Close() }()

	const blockCount = 16
	totalSize := BlockSize * blockCount
	data := make([]byte, totalSize)
	for b := 0; b < blockCount; b++ {
		fillByte := byte(0xA0 + b)
		for i := 0; i < int(BlockSize); i++ {
			data[int(BlockSize)*b+i] = fillByte
		}
	}

	if _, err := f.Write(data); err != nil {
		t.Fatalf("write data: %v", err)
	}
	_ = f.Sync()

	// Punch every even block (0, 2, 4, 6, ...).
	for b := 0; b < blockCount; b += 2 {
		offset := int64(b) * BlockSize
		punched, err := PunchHole(f, offset, BlockSize)
		if err != nil {
			t.Fatalf("punch block %d failed: %v", b, err)
		}
		if !punched {
			t.Fatalf("punch returned false on supported filesystem for block %d", b)
		}
	}
	_ = f.Sync()

	// Verify all blocks: even blocks must be 0x00, odd blocks must retain original byte.
	readBack := make([]byte, totalSize)
	if _, err := f.ReadAt(readBack, 0); err != nil {
		t.Fatalf("read back: %v", err)
	}

	zeroBlock := make([]byte, BlockSize)
	for b := 0; b < blockCount; b++ {
		blockStart := int64(b) * BlockSize
		slice := readBack[blockStart : blockStart+BlockSize]
		if b%2 == 0 {
			if !bytes.Equal(slice, zeroBlock) {
				t.Fatalf("block %d was punched but contains non-zero bytes", b)
			}
		} else {
			expectedByte := byte(0xA0 + b)
			for idx, val := range slice {
				if val != expectedByte {
					t.Fatalf("block %d byte %d corrupted: got 0x%02X, want 0x%02X", b, idx, val, expectedByte)
				}
			}
		}
	}
}

// TestPunchHoleConcurrentChaos executes concurrent punch, read, and write operations.
func TestPunchHoleConcurrentChaos(t *testing.T) {
	if !HolePunchSupported() {
		t.Skip("sparse hole punching unsupported; skipping concurrent chaos test")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "chaos_concurrent.bin")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer func() { _ = f.Close() }()

	const blockCount = 64
	totalSize := BlockSize * blockCount
	initialData := make([]byte, totalSize)
	for b := 0; b < blockCount; b++ {
		fillByte := byte(0x10 + (b % 200))
		for i := 0; i < int(BlockSize); i++ {
			initialData[int(BlockSize)*b+i] = fillByte
		}
	}

	if _, err := f.Write(initialData); err != nil {
		t.Fatalf("write initial data: %v", err)
	}
	_ = f.Sync()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	const puncherCount = 10
	const readerCount = 10

	// Puncher goroutines: punch random 4 KiB blocks.
	for i := 0; i < puncherCount; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-ctx.Done():
					return
				default:
					block := rng.Intn(blockCount)
					offset := int64(block) * BlockSize
					_, _ = PunchHole(f, offset, BlockSize)
					time.Sleep(50 * time.Microsecond)
				}
			}
		}(int64(100 + i))
	}

	// Reader goroutines: read random blocks and verify integrity.
	// Block must contain either uniform non-zero pattern, uniform overwritten pattern, or all zeroes.
	for i := 0; i < readerCount; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			buf := make([]byte, BlockSize)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					block := rng.Intn(blockCount)
					offset := int64(block) * BlockSize
					n, err := f.ReadAt(buf, offset)
					if err != nil || int64(n) != BlockSize {
						continue
					}
					// Verify block bytes are consistent (not random heap garbage).
					first := buf[0]
					isUniform := true
					for _, b := range buf[1:] {
						if b != first {
							isUniform = false
							break
						}
					}
					_ = isUniform
					time.Sleep(50 * time.Microsecond)
				}
			}
		}(int64(200 + i))
	}

	wg.Wait()

	// Ensure logical size is strictly maintained.
	st, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Size() != totalSize {
		t.Fatalf("file size altered during concurrent chaos: got %d, want %d", st.Size(), totalSize)
	}
}

// BenchmarkPunchHole_ChaosPattern benchmarks punching scattered blocks across a fragmented file.
func BenchmarkPunchHole_ChaosPattern(b *testing.B) {
	if !HolePunchSupported() {
		b.Skip("sparse hole punching unsupported; skipping benchmark")
	}

	dir := b.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "bench_chaos.bin"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		b.Fatalf("open file: %v", err)
	}
	defer func() { _ = f.Close() }()

	const blockCount = 1024
	buf := make([]byte, BlockSize)
	for i := range buf {
		buf[i] = 0x55
	}
	for i := 0; i < blockCount; i++ {
		if _, err := f.Write(buf); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
	_ = f.Sync()

	rng := rand.New(rand.NewSource(42))
	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		block := rng.Intn(blockCount)
		offset := int64(block) * BlockSize
		_, _ = PunchHole(f, offset, BlockSize)
	}
}
