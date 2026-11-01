package kernellane

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReclaimerValidation(t *testing.T) {
	// Nil file validation.
	if _, err := NewReclaimer(nil, ReclaimOptions{}); err == nil {
		t.Fatalf("expected error for nil source file")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "source.bin")
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer func() { _ = f.Close() }()

	// Negative lookback window validation.
	if _, err := NewReclaimer(f, ReclaimOptions{LookbackWindow: -1}); err == nil {
		t.Fatalf("expected error for negative lookback window")
	}

	r, err := NewReclaimer(f, ReclaimOptions{
		LookbackWindow:   LookbackWindowDeflate,
		MinPunchInterval: BlockSize,
	})
	if err != nil {
		t.Fatalf("new reclaimer: %v", err)
	}

	// Backwards source offset.
	_, _ = r.Commit(100, 100, "")
	if _, err := r.Commit(50, 100, ""); err == nil {
		t.Fatalf("expected error for backwards source offset")
	}

	// Backwards destination offset.
	if _, err := r.Commit(100, 50, ""); err == nil {
		t.Fatalf("expected error for backwards destination offset")
	}

	// Operations after close.
	if err := r.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Calling close a second time is an idempotent safe no-op.
	if err := r.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := r.Commit(200, 200, ""); err == nil {
		t.Fatalf("expected error after close")
	}

	// Closed file Stat error on NewReclaimer.
	closedFile, err := os.Create(filepath.Join(dir, "closed.bin"))
	if err != nil {
		t.Fatalf("create closed: %v", err)
	}
	_ = closedFile.Close()
	if _, err := NewReclaimer(closedFile, ReclaimOptions{}); err == nil {
		t.Fatalf("expected error from NewReclaimer on closed file")
	}
}

func TestReclaimerLookbackWindowAndLag(t *testing.T) {
	if !HolePunchSupported() {
		t.Skip("hole punching unsupported on host temp directory; skipping lookback lag test")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "lookback.bin")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open file: %v", err)
	}
	defer func() { _ = f.Close() }()

	const totalBlocks = 32
	totalSize := BlockSize * totalBlocks
	data := make([]byte, totalSize)
	for i := range data {
		data[i] = byte(0x20 + (i % 200))
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write data: %v", err)
	}
	_ = f.Sync()

	const lookback = BlockSize * 8 // 32 KiB lookback window
	r, err := NewReclaimer(f, ReclaimOptions{
		LookbackWindow:   lookback,
		MinPunchInterval: BlockSize * 4,
	})
	if err != nil {
		t.Fatalf("new reclaimer: %v", err)
	}

	// Commit offset equal to lookback. Safe punch offset is 0. No punch should happen.
	punched, err := r.Commit(lookback, lookback, "")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if punched {
		t.Fatalf("expected punched=false when sourceReadOffset equals lookback")
	}
	if r.Watermark().SourcePunchedOffset != 0 {
		t.Fatalf("expected punched offset 0, got %d", r.Watermark().SourcePunchedOffset)
	}

	// Commit offset equal to lookback + 4 blocks.
	// Safe punch offset is 4 blocks. MinPunchInterval is 4 blocks. Punch should trigger.
	punched, err = r.Commit(lookback+BlockSize*4, lookback+BlockSize*4, "")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !punched {
		t.Fatalf("expected punched=true after exceeding lookback and min interval")
	}
	if r.Watermark().SourcePunchedOffset != BlockSize*4 {
		t.Fatalf("expected punched offset %d, got %d", BlockSize*4, r.Watermark().SourcePunchedOffset)
	}

	// Verify data: first 4 blocks must be 0x00; remaining blocks must match original data.
	readBack := make([]byte, totalSize)
	if _, err := f.ReadAt(readBack, 0); err != nil {
		t.Fatalf("read back: %v", err)
	}

	zeroBlock := make([]byte, BlockSize*4)
	if !bytes.Equal(readBack[:BlockSize*4], zeroBlock) {
		t.Fatalf("expected first 4 blocks to be zeroed")
	}
	if !bytes.Equal(readBack[BlockSize*4:], data[BlockSize*4:]) {
		t.Fatalf("lookback protected window or suffix corrupted")
	}
}

func TestReclaimerAbortAndCleanup(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.bin")
	destPath := filepath.Join(dir, "dest.bin")
	ckptPath := filepath.Join(dir, "ckpt.json")

	if err := os.WriteFile(sourcePath, make([]byte, BlockSize*8), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(destPath, []byte("partial output"), 0o600); err != nil {
		t.Fatalf("write dest: %v", err)
	}

	f, err := os.OpenFile(sourcePath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer func() { _ = f.Close() }()

	r, err := NewReclaimer(f, ReclaimOptions{
		LookbackWindow:   BlockSize,
		MinPunchInterval: BlockSize,
		CheckpointPath:   ckptPath,
	})
	if err != nil {
		t.Fatalf("new reclaimer: %v", err)
	}

	// Abort with cleanup function that removes partial output file.
	cleanupExecuted := false
	err = r.Abort(func() error {
		cleanupExecuted = true
		return os.Remove(destPath)
	})
	if err != nil {
		t.Fatalf("abort: %v", err)
	}

	if !cleanupExecuted {
		t.Fatalf("expected cleanup function to execute")
	}
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Fatalf("expected destination file to be deleted on abort")
	}

	// Subsequent commits must fail with ErrReclaimerAborted.
	_, err = r.Commit(BlockSize*4, BlockSize*4, "")
	if err != ErrReclaimerAborted {
		t.Fatalf("expected ErrReclaimerAborted, got %v", err)
	}

	// Watermark checkpoint must record aborted status.
	wm, err := LoadCheckpoint(ckptPath)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if !wm.Aborted {
		t.Fatalf("expected checkpoint Aborted=true")
	}
}

func TestReclaimerInspectPunchedPrefix(t *testing.T) {
	// Nil file check.
	if _, err := InspectPunchedPrefix(nil); err == nil {
		t.Fatalf("expected error on nil file")
	}

	dir := t.TempDir()

	// Empty file.
	emptyPath := filepath.Join(dir, "empty.bin")
	ef, err := os.Create(emptyPath)
	if err != nil {
		t.Fatalf("create empty: %v", err)
	}
	defer func() { _ = ef.Close() }()
	punched, err := InspectPunchedPrefix(ef)
	if err != nil || punched {
		t.Fatalf("empty file: expected punched=false, got %v, err=%v", punched, err)
	}

	// Unpunched file with non-zero data.
	validPath := filepath.Join(dir, "valid.bin")
	vf, err := os.Create(validPath)
	if err != nil {
		t.Fatalf("create valid: %v", err)
	}
	defer func() { _ = vf.Close() }()
	if _, err := vf.Write([]byte("valid content header")); err != nil {
		t.Fatalf("write valid: %v", err)
	}
	punched, err = InspectPunchedPrefix(vf)
	if err != nil || punched {
		t.Fatalf("valid file: expected punched=false, got %v, err=%v", punched, err)
	}

	// File with zeroed first block.
	zeroPath := filepath.Join(dir, "zeroed.bin")
	zf, err := os.Create(zeroPath)
	if err != nil {
		t.Fatalf("create zero: %v", err)
	}
	defer func() { _ = zf.Close() }()
	if _, err := zf.Write(make([]byte, BlockSize)); err != nil {
		t.Fatalf("write zero: %v", err)
	}
	// Written zeros are data, not a hole. Only the zero-byte fallback reports them as punched.
	punched, err = InspectPunchedPrefix(zf)
	if want := !extentQueryable(t, zf); err != nil || punched != want {
		t.Fatalf("zeroed file: punched=%v err=%v, want punched=%v", punched, err, want)
	}

	// A real hole at block 0 is punched on every platform.
	pf := makePunchedSource(t, filepath.Join(dir, "punched.bin"), BlockSize*2)
	punched, err = InspectPunchedPrefix(pf)
	if err != nil || !punched {
		t.Fatalf("punched file: expected punched=true, got %v, err=%v", punched, err)
	}
}

// extentQueryable reports whether f's filesystem answers SEEK_DATA.
func extentQueryable(t *testing.T, f *os.File) bool {
	t.Helper()
	st, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	_, supported, err := kernelHoleAtStart(f, st.Size())
	if err != nil {
		t.Fatalf("kernelHoleAtStart: %v", err)
	}
	return supported
}

// makePunchedSource writes non-zero data and frees block 0.
// Where the filesystem cannot punch or query extents, block 0 is zeroed for the fallback check.
func makePunchedSource(t *testing.T, path string, size int64) *os.File {
	t.Helper()
	payload := bytes.Repeat([]byte{0x55}, int(size))
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	punched, err := PunchHole(f, 0, BlockSize)
	if err != nil {
		t.Fatalf("punch: %v", err)
	}
	if punched {
		return f
	}
	if extentQueryable(t, f) {
		t.Skip("filesystem answers SEEK_DATA but cannot punch; no real hole to inspect")
	}
	if _, err := f.WriteAt(make([]byte, BlockSize), 0); err != nil {
		t.Fatalf("zero block 0: %v", err)
	}
	return f
}

func TestReclaimerRestartValidation(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.bin")
	ckptPath := filepath.Join(dir, "ckpt.json")

	// Create a source file whose first block is a hole.
	payload := make([]byte, BlockSize*4)
	f := makePunchedSource(t, sourcePath, int64(len(payload)))

	// Restart without checkpoint: must return ErrUnrecoverableSparseSource.
	_, err := ValidateSourceForRestart(f, "")
	if err != ErrUnrecoverableSparseSource {
		t.Fatalf("expected ErrUnrecoverableSparseSource, got %v", err)
	}

	// Save valid checkpoint.
	wmSaved := ReclaimWatermark{
		SourceTotalSize:     int64(len(payload)),
		SourceReadOffset:    BlockSize * 2,
		SourcePunchedOffset: BlockSize,
		DestWrittenOffset:   BlockSize * 3,
		ChecksumHex:         "abcdef123456",
		Aborted:             false,
	}
	r, err := NewReclaimer(f, ReclaimOptions{
		LookbackWindow: 0,
		CheckpointPath: ckptPath,
	})
	if err != nil {
		t.Fatalf("new reclaimer: %v", err)
	}
	r.sourceReadOffset = wmSaved.SourceReadOffset
	r.sourcePunchedOffset = wmSaved.SourcePunchedOffset
	r.destWrittenOffset = wmSaved.DestWrittenOffset
	r.checksumHex = wmSaved.ChecksumHex
	if err := r.saveCheckpointLocked(); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	// Restart with valid checkpoint: restores watermark.
	wmRestored, err := ValidateSourceForRestart(f, ckptPath)
	if err != nil {
		t.Fatalf("validate for restart: %v", err)
	}
	if wmRestored.SourceReadOffset != wmSaved.SourceReadOffset {
		t.Fatalf("read offset: got %d, want %d", wmRestored.SourceReadOffset, wmSaved.SourceReadOffset)
	}
	if wmRestored.SourcePunchedOffset != wmSaved.SourcePunchedOffset {
		t.Fatalf("punched offset: got %d, want %d", wmRestored.SourcePunchedOffset, wmSaved.SourcePunchedOffset)
	}
	if wmRestored.DestWrittenOffset != wmSaved.DestWrittenOffset {
		t.Fatalf("dest written: got %d, want %d", wmRestored.DestWrittenOffset, wmSaved.DestWrittenOffset)
	}
}

func TestReclaimerSteadyStateAllocations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "alloc.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	defer func() { _ = f.Close() }()

	r, err := NewReclaimer(f, ReclaimOptions{
		LookbackWindow:   BlockSize * 4,
		MinPunchInterval: BlockSize * 100, // Large interval to test steady state loops
	})
	if err != nil {
		t.Fatalf("new reclaimer: %v", err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		_, _ = r.Commit(BlockSize*2, BlockSize*2, "")
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs/op during steady state commit, got %f", allocs)
	}
}

func TestReclaimerCheckpointAndRestartEdgeCases(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "edge_source.bin")
	if err := os.WriteFile(sourcePath, []byte("unpunched regular file content"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	f, err := os.OpenFile(sourcePath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer func() { _ = f.Close() }()

	// Restart with empty checkpoint path on unpunched file: returns offset 0 watermark.
	wm, err := ValidateSourceForRestart(f, "")
	if err != nil {
		t.Fatalf("validate unpunched file: %v", err)
	}
	if wm.SourceReadOffset != 0 || wm.SourceTotalSize != int64(len("unpunched regular file content")) {
		t.Fatalf("unexpected unpunched watermark: %+v", wm)
	}

	// LoadCheckpoint on non-existent path.
	if _, err := LoadCheckpoint(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatalf("expected error loading missing checkpoint")
	}

	// LoadCheckpoint with corrupted JSON.
	corruptCkpt := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(corruptCkpt, []byte("{invalid json content"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, err := LoadCheckpoint(corruptCkpt); err == nil {
		t.Fatalf("expected error loading corrupt checkpoint")
	}

	// Save checkpoint to impossible directory path.
	r, err := NewReclaimer(f, ReclaimOptions{
		LookbackWindow: 0,
		CheckpointPath: filepath.Join(dir, "impossible_dir", "sub", "ckpt.json"),
	})
	if err != nil {
		t.Fatalf("new reclaimer: %v", err)
	}
	if err := r.saveCheckpointLocked(); err == nil {
		t.Fatalf("expected error saving checkpoint to invalid directory")
	}
}

func TestReclaimerSmallFileInspectPrefix(t *testing.T) {
	dir := t.TempDir()

	// Small file (< BlockSize) with all zeroes.
	zeroSmall := filepath.Join(dir, "small_zero.bin")
	if err := os.WriteFile(zeroSmall, make([]byte, 128), 0o600); err != nil {
		t.Fatalf("write small zero: %v", err)
	}
	zf, err := os.Open(zeroSmall)
	if err != nil {
		t.Fatalf("open small zero: %v", err)
	}
	defer func() { _ = zf.Close() }()

	// A file smaller than one block cannot hold a hole. Only the zero-byte fallback reports one.
	punched, err := InspectPunchedPrefix(zf)
	if want := !extentQueryable(t, zf); err != nil || punched != want {
		t.Fatalf("small zero file: punched=%v err=%v, want punched=%v", punched, err, want)
	}

	// Small file (< BlockSize) with non-zero byte.
	nonzeroSmall := filepath.Join(dir, "small_nonzero.bin")
	content := make([]byte, 128)
	content[64] = 0xFF
	if err := os.WriteFile(nonzeroSmall, content, 0o600); err != nil {
		t.Fatalf("write small nonzero: %v", err)
	}
	nf, err := os.Open(nonzeroSmall)
	if err != nil {
		t.Fatalf("open small nonzero: %v", err)
	}
	defer func() { _ = nf.Close() }()

	punched, err = InspectPunchedPrefix(nf)
	if err != nil || punched {
		t.Fatalf("small nonzero file: got punched=%v, err=%v", punched, err)
	}
}

type reclaimCountingReader struct {
	r io.Reader
	n int64
}

func (c *reclaimCountingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// TestReclaimerStreamingDecompressCrashRecovery simulates in-place decompression with fatal errors and recovery.
func TestReclaimerStreamingDecompressCrashRecovery(t *testing.T) {
	if !HolePunchSupported() {
		t.Skip("hole punching unsupported on host temp directory; skipping streaming crash recovery test")
	}

	dir := t.TempDir()
	sourceGzPath := filepath.Join(dir, "archive.gz")
	destPath := filepath.Join(dir, "extracted.bin")
	ckptPath := filepath.Join(dir, "reclaim_ckpt.json")

	// Create 1 MiB mixed-entropy test payload so compressed size exceeds lookback window.
	const payloadSize = 1024 * 1024
	originalPayload := make([]byte, payloadSize)
	rng := rand.New(rand.NewSource(42))
	for i := range originalPayload {
		originalPayload[i] = byte(rng.Intn(128))
	}
	expectedChecksum := sha256.Sum256(originalPayload)
	expectedChecksumHex := hex.EncodeToString(expectedChecksum[:])

	// Compress payload to gzip format.
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	if _, err := gw.Write(originalPayload); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	gzBytes := gzBuf.Bytes()
	if int64(len(gzBytes)) < LookbackWindowDeflate*4 {
		t.Fatalf("compressed size %d too small for lookback punch test", len(gzBytes))
	}

	if err := os.WriteFile(sourceGzPath, gzBuf.Bytes(), 0o600); err != nil {
		t.Fatalf("write gz file: %v", err)
	}

	t.Run("midstream_fatal_error_cleans_up_and_aborts", func(t *testing.T) {
		sf, err := os.OpenFile(sourceGzPath, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open source: %v", err)
		}
		defer func() { _ = sf.Close() }()

		df, err := os.OpenFile(destPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
		if err != nil {
			t.Fatalf("open dest: %v", err)
		}
		defer func() { _ = df.Close() }()

		rec, err := NewReclaimer(sf, ReclaimOptions{
			LookbackWindow: LookbackWindowDeflate,
			CheckpointPath: ckptPath,
		})
		if err != nil {
			t.Fatalf("NewReclaimer: %v", err)
		}

		countingR := &reclaimCountingReader{r: sf}
		gzr, err := gzip.NewReader(countingR)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}

		// Read half the payload then inject abort.
		buf := make([]byte, 32*1024)
		var written int64
		for written < payloadSize/2 {
			n, err := gzr.Read(buf)
			if n > 0 {
				if _, werr := df.Write(buf[:n]); werr != nil {
					t.Fatalf("write: %v", werr)
				}
				written += int64(n)
				if _, perr := rec.Commit(countingR.n, written, ""); perr != nil {
					t.Fatalf("commit: %v", perr)
				}
			}
			if err != nil {
				break
			}
		}

		// Inject fatal abort.
		abortErr := rec.Abort(func() error {
			return os.Remove(destPath)
		})
		if abortErr != nil {
			t.Fatalf("abort failed: %v", abortErr)
		}

		// Destination file must be deleted.
		if _, err := os.Stat(destPath); !os.IsNotExist(err) {
			t.Fatalf("destination file must be cleaned up after abort: %v", err)
		}

		// Watermark must record aborted state.
		wm := rec.Watermark()
		if !wm.Aborted {
			t.Fatalf("expected watermark to be aborted")
		}

		// An aborted reclaimer refuses further commits.
		if _, err := rec.Commit(BlockSize*4, BlockSize*4, ""); !errors.Is(err, ErrReclaimerAborted) {
			t.Fatalf("commit after abort: got %v, want ErrReclaimerAborted", err)
		}

		// A restart must see the aborted checkpoint and refuse to resume.
		if restartWM, err := ValidateSourceForRestart(sf, ckptPath); !errors.Is(err, ErrReclaimerAborted) {
			t.Fatalf("restart after abort: got %v (wm %+v), want ErrReclaimerAborted", err, restartWM)
		}
	})

	t.Run("successful_complete_preserves_reclaimed_integrity", func(t *testing.T) {
		if err := os.WriteFile(sourceGzPath, gzBuf.Bytes(), 0o600); err != nil {
			t.Fatalf("write gz file: %v", err)
		}
		sf, err := os.OpenFile(sourceGzPath, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open source: %v", err)
		}
		defer func() { _ = sf.Close() }()

		df, err := os.OpenFile(destPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
		if err != nil {
			t.Fatalf("open dest: %v", err)
		}
		defer func() { _ = df.Close() }()

		rec, err := NewReclaimer(sf, ReclaimOptions{
			LookbackWindow: LookbackWindowDeflate,
			CheckpointPath: ckptPath,
		})
		if err != nil {
			t.Fatalf("NewReclaimer: %v", err)
		}

		countingR := &reclaimCountingReader{r: sf}
		gzr, err := gzip.NewReader(countingR)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}

		buf := make([]byte, 32*1024)
		var written int64
		for {
			n, err := gzr.Read(buf)
			if n > 0 {
				if _, werr := df.Write(buf[:n]); werr != nil {
					t.Fatalf("write: %v", werr)
				}
				written += int64(n)
				if _, perr := rec.Commit(countingR.n, written, ""); perr != nil {
					t.Fatalf("commit: %v", perr)
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("decompression failed: %v", err)
			}
		}

		if err := rec.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}

		wm := rec.Watermark()
		if wm.SourcePunchedOffset <= 0 {
			t.Fatalf("expected punched offset > 0, got %d", wm.SourcePunchedOffset)
		}
		if wm.DestWrittenOffset != payloadSize {
			t.Fatalf("dest written offset: got %d, want %d", wm.DestWrittenOffset, payloadSize)
		}

		// Verify extracted content bit-for-bit.
		extracted, err := os.ReadFile(destPath)
		if err != nil {
			t.Fatalf("read extracted: %v", err)
		}
		actualChecksum := sha256.Sum256(extracted)
		actualChecksumHex := hex.EncodeToString(actualChecksum[:])
		if actualChecksumHex != expectedChecksumHex {
			t.Fatalf("checksum mismatch: got %s, want %s", actualChecksumHex, expectedChecksumHex)
		}

		// Verify source file prefix was physically reclaimed.
		punched, err := InspectPunchedPrefix(sf)
		if err != nil {
			t.Fatalf("InspectPunchedPrefix: %v", err)
		}
		if !punched {
			t.Fatalf("expected source file prefix to be reclaimed")
		}
	})

	// Safety proof for punching behind the read offset. The pre-move version of
	// this subtest punched bytes the decompressor had already consumed, which is
	// harmless (it keeps its window in memory), and it counted io.EOF as a
	// failure, so it always passed. This version punches past the consumed
	// offset, as an eager reclaimer that ignores the read watermark would.
	t.Run("punching_ahead_of_the_read_offset_corrupts_decompression", func(t *testing.T) {
		eagerPath := filepath.Join(dir, "eager_punch_archive.gz")
		if err := os.WriteFile(eagerPath, gzBytes, 0o600); err != nil {
			t.Fatalf("write eager gz: %v", err)
		}
		sf, err := os.OpenFile(eagerPath, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open eager source: %v", err)
		}
		defer func() { _ = sf.Close() }()

		counting := &reclaimCountingReader{r: sf}
		zr, err := gzip.NewReader(counting)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		if _, err := zr.Read(make([]byte, 16*1024)); err != nil {
			t.Fatalf("first read: %v", err)
		}
		consumed := counting.n
		punchTo := (consumed/BlockSize + 8) * BlockSize
		if punchTo >= int64(len(gzBytes)) {
			t.Fatalf("archive too small: punch %d >= size %d", punchTo, len(gzBytes))
		}
		if _, err := PunchHolePrefix(sf, punchTo); err != nil {
			t.Fatalf("eager punch: %v", err)
		}

		drain := make([]byte, 32*1024)
		var readErr error
		for range 200 {
			if _, readErr = zr.Read(drain); readErr != nil {
				break
			}
		}
		if readErr == nil || errors.Is(readErr, io.EOF) {
			t.Fatalf("punching unread source bytes must corrupt decompression, got %v", readErr)
		}
	})

	// Reclaiming only behind the read offset keeps the stream intact; the
	// successful_complete subtest above covers that path end to end.

	t.Run("cold_crash_checkpoint_restores_and_reclaims_safely", func(t *testing.T) {
		sf, err := os.OpenFile(sourceGzPath, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open source: %v", err)
		}
		defer func() { _ = sf.Close() }()

		rec, err := NewReclaimer(sf, ReclaimOptions{
			LookbackWindow: LookbackWindowDeflate,
			CheckpointPath: ckptPath,
		})
		if err != nil {
			t.Fatalf("NewReclaimer: %v", err)
		}

		// Write checkpoint simulating 64 KiB read with 32 KiB punched.
		rec.sourceReadOffset = 64 * 1024
		rec.sourcePunchedOffset = 32 * 1024
		rec.destWrittenOffset = 64 * 1024
		if err := rec.saveCheckpointLocked(); err != nil {
			t.Fatalf("saveCheckpointLocked: %v", err)
		}

		// Reload from checkpoint.
		wm, err := ValidateSourceForRestart(sf, ckptPath)
		if err != nil {
			t.Fatalf("ValidateSourceForRestart with checkpoint: %v", err)
		}
		if wm.SourceReadOffset != 64*1024 {
			t.Fatalf("expected read offset 64 KiB, got %d", wm.SourceReadOffset)
		}
		if wm.SourcePunchedOffset != 32*1024 {
			t.Fatalf("expected punched offset 32 KiB, got %d", wm.SourcePunchedOffset)
		}
	})
}

// Regression: Abort runs cleanup once, and never after Close.
func TestReclaimerAbortTransitions(t *testing.T) {
	newReclaimer := func(t *testing.T) *Reclaimer {
		t.Helper()
		path := filepath.Join(t.TempDir(), "source.bin")
		if err := os.WriteFile(path, make([]byte, BlockSize*4), 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open source: %v", err)
		}
		t.Cleanup(func() { _ = f.Close() })
		r, err := NewReclaimer(f, ReclaimOptions{})
		if err != nil {
			t.Fatalf("new reclaimer: %v", err)
		}
		return r
	}

	t.Run("second abort does not run cleanup again", func(t *testing.T) {
		r := newReclaimer(t)
		runs := 0
		cleanup := func() error { runs++; return nil }
		if err := r.Abort(cleanup); err != nil {
			t.Fatalf("first abort: %v", err)
		}
		if err := r.Abort(cleanup); err != nil {
			t.Fatalf("second abort: %v", err)
		}
		if runs != 1 {
			t.Fatalf("cleanup ran %d times, want 1", runs)
		}
	})

	t.Run("abort after close is refused", func(t *testing.T) {
		r := newReclaimer(t)
		if err := r.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		ran := false
		if err := r.Abort(func() error { ran = true; return nil }); !errors.Is(err, ErrReclaimerClosed) {
			t.Fatalf("abort after close = %v, want ErrReclaimerClosed", err)
		}
		if ran || r.Watermark().Aborted {
			t.Fatal("abort after close changed state or ran cleanup")
		}
		if _, err := r.Commit(BlockSize, BlockSize, ""); !errors.Is(err, ErrReclaimerClosed) {
			t.Fatalf("commit after close = %v, want ErrReclaimerClosed", err)
		}
	})

	t.Run("close after abort is legal", func(t *testing.T) {
		r := newReclaimer(t)
		if err := r.Abort(nil); err != nil {
			t.Fatalf("abort: %v", err)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("close after abort: %v", err)
		}
		if !r.Watermark().Aborted {
			t.Fatal("watermark lost the aborted mark")
		}
	})
}

// Regression: Commit is write-ahead. A checkpoint that cannot be saved must free nothing.
func TestReclaimerCommitDoesNotPunchWhenCheckpointFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.bin")
	payload := bytes.Repeat([]byte{0x55}, int(BlockSize*8))
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	defer func() { _ = f.Close() }()
	r, err := NewReclaimer(f, ReclaimOptions{
		MinPunchInterval: BlockSize,
		CheckpointPath:   filepath.Join(dir, "missing-dir", "ckpt.json"),
	})
	if err != nil {
		t.Fatalf("new reclaimer: %v", err)
	}
	punched, err := r.Commit(BlockSize*4, BlockSize*4, "")
	if err == nil || punched {
		t.Fatalf("Commit with unsavable checkpoint = (%v, %v), want (false, error)", punched, err)
	}
	if wm := r.Watermark(); wm.SourcePunchedOffset != 0 {
		t.Fatalf("punched offset advanced to %d without a durable checkpoint", wm.SourcePunchedOffset)
	}
	got := make([]byte, BlockSize)
	if _, err := f.ReadAt(got, 0); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload[:BlockSize]) {
		t.Fatal("source block 0 was freed although the checkpoint failed")
	}
}

func TestReclaimerCheckpointCoversPunch(t *testing.T) {
	dir := t.TempDir()
	ckpt := filepath.Join(dir, "ckpt.json")
	f := makePunchedSourceFile(t, filepath.Join(dir, "source.bin"), BlockSize*8)
	r, err := NewReclaimer(f, ReclaimOptions{MinPunchInterval: BlockSize, CheckpointPath: ckpt})
	if err != nil {
		t.Fatalf("new reclaimer: %v", err)
	}
	if _, err := r.Commit(BlockSize*4, BlockSize*4, "abc"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	wm, err := LoadCheckpoint(ckpt)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if wm.SourcePunchedOffset != BlockSize*4 || wm.SourceReadOffset != BlockSize*4 || wm.ChecksumHex != "abc" {
		t.Fatalf("checkpoint = %+v, want punched and read offsets at %d", wm, BlockSize*4)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary checkpoint %q was left behind", e.Name())
		}
	}
}

// makePunchedSourceFile writes non-zero data and returns the open file without punching it.
func makePunchedSourceFile(t *testing.T, path string, size int64) *os.File {
	t.Helper()
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x55}, int(size)), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open source: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestFirstBlockReadsZero(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) *os.File {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	}
	zeros := write("zeros.bin", make([]byte, BlockSize*2))
	if got, err := firstBlockReadsZero(zeros, BlockSize*2); err != nil || !got {
		t.Fatalf("zero block = (%v, %v), want (true, nil)", got, err)
	}
	small := write("small.bin", make([]byte, 100))
	if got, err := firstBlockReadsZero(small, 100); err != nil || !got {
		t.Fatalf("small zero file = (%v, %v), want (true, nil)", got, err)
	}
	data := write("data.bin", bytes.Repeat([]byte{1}, int(BlockSize)))
	if got, err := firstBlockReadsZero(data, BlockSize); err != nil || got {
		t.Fatalf("data block = (%v, %v), want (false, nil)", got, err)
	}
	closed := write("closed.bin", make([]byte, BlockSize))
	_ = closed.Close()
	if _, err := firstBlockReadsZero(closed, BlockSize); err == nil {
		t.Fatal("read of closed file returned nil error")
	}
}

// A file extended by Truncate has no data extents. SEEK_DATA reports ENXIO, which is a hole.
func TestInspectPunchedPrefixSparseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparse.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(BlockSize * 4); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	punched, err := InspectPunchedPrefix(f)
	if err != nil || !punched {
		t.Fatalf("sparse file = (%v, %v), want (true, nil)", punched, err)
	}
	if pos, err := f.Seek(0, io.SeekCurrent); err != nil || pos != 0 {
		t.Fatalf("file offset after inspect = (%d, %v), want (0, nil)", pos, err)
	}
}

func TestInspectPunchedPrefixErrors(t *testing.T) {
	if _, err := InspectPunchedPrefix(nil); err == nil {
		t.Fatal("nil file returned nil error")
	}
	path := filepath.Join(t.TempDir(), "closed.bin")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = f.Close()
	if _, err := InspectPunchedPrefix(f); err == nil {
		t.Fatal("closed file returned nil error")
	}
	if _, _, err := kernelHoleAtStart(f, 1); err == nil && extentQueryableClosedFileIsError() {
		t.Fatal("kernelHoleAtStart on closed file returned nil error")
	}
}

// extentQueryableClosedFileIsError reports whether this platform queries extents at all.
func extentQueryableClosedFileIsError() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}

func TestWriteFileDurableErrors(t *testing.T) {
	dir := t.TempDir()
	if err := writeFileDurable(filepath.Join(dir, "missing", "ckpt.json"), []byte("{}")); err == nil {
		t.Fatal("write into missing directory returned nil error")
	}
	// Rename onto a non-empty directory fails, and the temporary file must be removed.
	target := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFileDurable(target, []byte("{}")); err == nil {
		t.Fatal("rename onto a non-empty directory returned nil error")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file %q was left behind", e.Name())
		}
	}
	if err := syncDir(filepath.Join(dir, "missing")); err == nil && runtime.GOOS != "windows" {
		t.Fatal("syncDir on a missing directory returned nil error")
	}
	if err := syncDir(dir); err != nil {
		t.Fatalf("syncDir: %v", err)
	}
}
