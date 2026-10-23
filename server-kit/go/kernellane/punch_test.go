package kernellane

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
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
	for i := 0; i < b.N; i++ {
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
			for i := 0; i < 4; i++ {
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
