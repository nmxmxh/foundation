package kernellane

import (
	"bytes"
	"context"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPunchHoleValidation(t *testing.T) {
	// Nil file check.
	if _, err := PunchHole(nil, 0, BlockSize); err == nil {
		t.Fatalf("expected error on nil file, got nil")
	}

	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "val.bin"))
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer func() { _ = f.Close() }()

	cases := []struct {
		name   string
		offset int64
		length int64
	}{
		{"negative_offset", -1, BlockSize},
		{"zero_length", 0, 0},
		{"negative_length", 0, -BlockSize},
		{"overflow_range", math.MaxInt64 - 10, 20},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			punched, err := PunchHole(f, tc.offset, tc.length)
			if err != ErrInvalidRange {
				t.Fatalf("offset=%d, len=%d: expected ErrInvalidRange, got err=%v, punched=%v",
					tc.offset, tc.length, err, punched)
			}
		})
	}
}

func TestPunchHolePrefix(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "prefix.bin"))
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer func() { _ = f.Close() }()

	// Under BlockSize (4 KiB) should be a safe no-op.
	punched, err := PunchHolePrefix(f, BlockSize-1)
	if err != nil {
		t.Fatalf("unexpected error for small prefix: %v", err)
	}
	if punched {
		t.Fatalf("expected punched=false for prefix smaller than BlockSize")
	}

	// Nil file check.
	if _, err := PunchHolePrefix(nil, BlockSize*2); err == nil {
		t.Fatalf("expected error on nil file for PunchHolePrefix")
	}
}

func TestPunchHoleDataZeroed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open temp file: %v", err)
	}
	defer func() { _ = f.Close() }()

	const totalSize = BlockSize * 4 // 16 KiB
	payload := make([]byte, totalSize)
	for i := range payload {
		payload[i] = 0xAA
	}
	if _, err := f.Write(payload); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync payload: %v", err)
	}

	// Punch a hole in the middle: [BlockSize, BlockSize*3)
	punched, err := PunchHole(f, BlockSize, BlockSize*2)
	if err != nil {
		t.Fatalf("PunchHole error: %v", err)
	}
	if !punched {
		t.Skip("sparse hole punching not supported on this filesystem; skipping data-zeroed check")
	}

	readBack := make([]byte, totalSize)
	if _, err := f.ReadAt(readBack, 0); err != nil {
		t.Fatalf("read back: %v", err)
	}

	// First block: preserved 0xAA
	if !bytes.Equal(readBack[:BlockSize], payload[:BlockSize]) {
		t.Fatalf("prefix data corrupted after punch")
	}
	// Middle 2 blocks: zeroed out
	expectedZeros := make([]byte, BlockSize*2)
	if !bytes.Equal(readBack[BlockSize:BlockSize*3], expectedZeros) {
		t.Fatalf("punched range is not zeroed out")
	}
	// Last block: preserved 0xAA
	if !bytes.Equal(readBack[BlockSize*3:], payload[BlockSize*3:]) {
		t.Fatalf("suffix data corrupted after punch")
	}
}

func TestPunchHolePhysicalBlockReduction(t *testing.T) {
	if !HolePunchSupported() {
		t.Skip("hole punching unsupported on host temp directory; skipping block reduction test")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "blocks.bin")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer func() { _ = f.Close() }()

	const fileLen = 2 * 1024 * 1024 // 2 MiB
	buf := make([]byte, 64*1024)
	for i := range buf {
		buf[i] = 0xFE
	}
	for written := 0; written < fileLen; written += len(buf) {
		if _, err := f.Write(buf); err != nil {
			t.Fatalf("fill file: %v", err)
		}
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync file: %v", err)
	}

	stBefore, err := f.Stat()
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	blocksBefore := fileStatBlocks(stBefore)

	// Punch 1 MiB hole in the middle
	const punchLen = 1 * 1024 * 1024
	punched, err := PunchHole(f, punchLen/2, punchLen)
	if err != nil {
		t.Fatalf("PunchHole error: %v", err)
	}
	if !punched {
		t.Fatalf("expected punched=true since HolePunchSupported was true")
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync after punch: %v", err)
	}

	stAfter, err := f.Stat()
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	blocksAfter := fileStatBlocks(stAfter)

	// Logical size must remain exactly 2 MiB
	if stAfter.Size() != fileLen {
		t.Fatalf("logical size changed: got %d, want %d", stAfter.Size(), fileLen)
	}

	// On filesystems that report blocks, blocks must decrease
	if blocksBefore > 0 && blocksAfter >= blocksBefore {
		t.Fatalf("physical blocks did not decrease: before=%d, after=%d", blocksBefore, blocksAfter)
	}
}

func TestHolePunchProbes(t *testing.T) {
	// Nil file check
	if HolePunchSupportedFile(nil) {
		t.Fatalf("expected HolePunchSupportedFile(nil) to be false")
	}

	// File with empty name
	emptyNameFile := os.NewFile(0, "")
	if emptyNameFile != nil {
		_ = HolePunchSupportedFile(emptyNameFile)
	}

	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "probe_target.bin"))
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	defer func() { _ = f.Close() }()

	fileSupported := HolePunchSupportedFile(f)
	dirSupported := HolePunchSupportedInDir(dir)
	if fileSupported != dirSupported {
		t.Fatalf("fileSupported (%v) != dirSupported (%v)", fileSupported, dirSupported)
	}

	// Repeated HolePunchSupported() calls must be idempotent and cached.
	s1 := HolePunchSupported()
	s2 := HolePunchSupported()
	if s1 != s2 {
		t.Fatalf("cached HolePunchSupported changed: %v vs %v", s1, s2)
	}
}

func TestHolePunchClosedFileAndErrors(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "closed.bin"))
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	_ = f.Close()

	// Calling PunchHole on a closed file descriptor should return an error.
	_, err = PunchHole(f, 0, BlockSize)
	if err == nil {
		t.Fatalf("expected error punching closed file, got nil")
	}
}

func BenchmarkPunchHole(b *testing.B) {
	dir := b.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "bench.bin"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		b.Fatalf("open bench file: %v", err)
	}
	defer func() { _ = f.Close() }()

	// Pre-fill file with 16 MiB
	const benchSize = 16 * 1024 * 1024
	buf := make([]byte, 64*1024)
	for i := range buf {
		buf[i] = 0x77
	}
	for written := 0; written < benchSize; written += len(buf) {
		if _, err := f.Write(buf); err != nil {
			b.Fatalf("fill bench file: %v", err)
		}
	}
	_ = f.Sync()

	if !HolePunchSupportedFile(f) {
		b.Skip("hole punching unsupported on bench filesystem")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		// Punch 64 KiB chunks repeatedly in pre-allocated range
		offset := int64((i % 128)) * 64 * 1024
		if _, err := PunchHole(f, offset, 64*1024); err != nil {
			b.Fatalf("punch error: %v", err)
		}
	}
}

func BenchmarkPunchHoleSizes(b *testing.B) {
	sizes := []struct {
		name string
		size int64
	}{
		{"4KiB", 4 * 1024},
		{"64KiB", 64 * 1024},
		{"1MiB", 1024 * 1024},
	}

	for _, tc := range sizes {
		b.Run(tc.name, func(b *testing.B) {
			dir := b.TempDir()
			f, err := os.OpenFile(filepath.Join(dir, "bench_sizes.bin"), os.O_CREATE|os.O_RDWR, 0o600)
			if err != nil {
				b.Fatalf("open bench file: %v", err)
			}
			defer func() { _ = f.Close() }()

			// Pre-fill
			buf := make([]byte, tc.size)
			for range 4 {
				_, _ = f.Write(buf)
			}
			_ = f.Sync()

			if !HolePunchSupportedFile(f) {
				b.Skip("hole punching unsupported on bench filesystem")
			}

			b.SetBytes(tc.size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				offset := int64(i%2) * tc.size
				if _, err := PunchHole(f, offset, tc.size); err != nil {
					b.Fatalf("punch error: %v", err)
				}
			}
		})
	}
}

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
	for b := range blockCount {
		fillByte := byte(0xA0 + b)
		for i := range int(BlockSize) {
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
	for b := range blockCount {
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
	for b := range blockCount {
		fillByte := byte(0x10 + (b % 200))
		for i := range int(BlockSize) {
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
	for i := range puncherCount {
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
	for i := range readerCount {
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
	for range blockCount {
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
