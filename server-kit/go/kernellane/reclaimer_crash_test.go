package kernellane

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

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

		r, err := NewReclaimer(sf, ReclaimOptions{
			LookbackWindow:   LookbackWindowDeflate,
			MinPunchInterval: BlockSize,
			CheckpointPath:   ckptPath,
		})
		if err != nil {
			t.Fatalf("new reclaimer: %v", err)
		}

		// Write partial corrupted chunk.
		_, _ = df.Write([]byte("partial corrupted chunk"))
		_ = df.Close()

		// Simulate fatal validation failure.
		err = r.Abort(func() error {
			return os.Remove(destPath)
		})
		if err != nil {
			t.Fatalf("abort: %v", err)
		}

		// Ensure destination is purged and commit is rejected.
		if _, err := os.Stat(destPath); !os.IsNotExist(err) {
			t.Fatalf("expected dest file to be unlinked after fatal error")
		}

		_, err = r.Commit(BlockSize*4, BlockSize*4, "")
		if err != ErrReclaimerAborted {
			t.Fatalf("expected ErrReclaimerAborted, got %v", err)
		}

		// Verify restart check catches aborted state.
		wm, err := ValidateSourceForRestart(sf, ckptPath)
		if err != ErrReclaimerAborted {
			t.Fatalf("expected ErrReclaimerAborted on restart, got %v (wm: %+v)", err, wm)
		}
	})

	t.Run("streaming_decompression_honors_lookback_and_verifies_checksum", func(t *testing.T) {
		// Fresh source archive.
		freshSourcePath := filepath.Join(dir, "fresh_archive.gz")
		if err := os.WriteFile(freshSourcePath, gzBuf.Bytes(), 0o600); err != nil {
			t.Fatalf("write fresh gz: %v", err)
		}

		sf, err := os.OpenFile(freshSourcePath, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open fresh source: %v", err)
		}
		defer func() { _ = sf.Close() }()

		df, err := os.OpenFile(destPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
		if err != nil {
			t.Fatalf("open dest: %v", err)
		}
		defer func() { _ = df.Close() }()

		freshCkptPath := filepath.Join(dir, "fresh_ckpt.json")
		r, err := NewReclaimer(sf, ReclaimOptions{
			LookbackWindow:   LookbackWindowDeflate,
			MinPunchInterval: BlockSize * 2,
			CheckpointPath:   freshCkptPath,
		})
		if err != nil {
			t.Fatalf("new reclaimer: %v", err)
		}

		// Stream decompress with two-phase commit and byte counter.
		cr := &reclaimCountingReader{r: sf}
		zr, err := gzip.NewReader(cr)
		if err != nil {
			t.Fatalf("gzip new reader: %v", err)
		}
		defer func() { _ = zr.Close() }()

		chunk := make([]byte, 32*1024)
		hasher := sha256.New()
		var totalExtracted int64

		for {
			n, readErr := zr.Read(chunk)
			if n > 0 {
				if _, err := df.Write(chunk[:n]); err != nil {
					_ = r.Abort(func() error { return os.Remove(destPath) })
					t.Fatalf("write chunk: %v", err)
				}
				hasher.Write(chunk[:n])
				totalExtracted += int64(n)

				// Commit read and write progress.
				_, commitErr := r.Commit(cr.n, totalExtracted, "")
				if commitErr != nil {
					t.Fatalf("reclaimer commit: %v", commitErr)
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				t.Fatalf("gzip read: %v", readErr)
			}
		}

		_ = df.Sync()

		// Verify extracted content hash matches original.
		actualChecksum := hasher.Sum(nil)
		actualChecksumHex := hex.EncodeToString(actualChecksum)
		if actualChecksumHex != expectedChecksumHex {
			t.Fatalf("checksum mismatch: got %s, want %s", actualChecksumHex, expectedChecksumHex)
		}

		if err := r.Close(); err != nil {
			t.Fatalf("reclaimer close: %v", err)
		}

		// Verify watermark shows successful extraction.
		wm := r.Watermark()
		if wm.DestWrittenOffset != payloadSize {
			t.Fatalf("dest written offset: got %d, want %d", wm.DestWrittenOffset, payloadSize)
		}
		if wm.SourcePunchedOffset <= 0 {
			t.Fatalf("expected physical blocks to be reclaimed, got punched offset %d", wm.SourcePunchedOffset)
		}
	})

	t.Run("eager_punching_without_lookback_causes_decompression_failure", func(t *testing.T) {
		corruptSourcePath := filepath.Join(dir, "eager_punch_archive.gz")
		if err := os.WriteFile(corruptSourcePath, gzBuf.Bytes(), 0o600); err != nil {
			t.Fatalf("write eager gz: %v", err)
		}

		sf, err := os.OpenFile(corruptSourcePath, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open corrupt source: %v", err)
		}
		defer func() { _ = sf.Close() }()

		zr, err := gzip.NewReader(sf)
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}

		// Read first chunk to get decompressor into stream.
		buf := make([]byte, 16*1024)
		if _, err := zr.Read(buf); err != nil {
			t.Fatalf("first read: %v", err)
		}

		// Punch the entire first block of the source archive eagerly (0 lag).
		_, punchErr := PunchHolePrefix(sf, BlockSize*4)
		if punchErr != nil {
			t.Fatalf("eager punch: %v", punchErr)
		}

		// Subsequent decompression must fail due to corrupted dictionary or zeroed input.
		drainBuf := make([]byte, 32*1024)
		var decompressFailed bool
		for i := 0; i < 100; i++ {
			_, err := zr.Read(drainBuf)
			if err != nil {
				decompressFailed = true
				break
			}
		}
		if !decompressFailed {
			t.Fatalf("expected eager hole punch without lookback lag to cause decompression failure")
		}
	})

	t.Run("naive_restart_from_zero_fails_while_checkpointed_restart_succeeds", func(t *testing.T) {
		punchedSourcePath := filepath.Join(dir, "punched_source.gz")
		if err := os.WriteFile(punchedSourcePath, gzBuf.Bytes(), 0o600); err != nil {
			t.Fatalf("write punched source: %v", err)
		}

		sf, err := os.OpenFile(punchedSourcePath, os.O_RDWR, 0o600)
		if err != nil {
			t.Fatalf("open punched source: %v", err)
		}
		defer func() { _ = sf.Close() }()

		// Punch the prefix.
		punched, err := PunchHolePrefix(sf, BlockSize*2)
		if err != nil || !punched {
			t.Fatalf("punch prefix failed: %v", err)
		}

		// Naive restart: reading from byte 0 into gzip reader must fail with invalid header.
		_, seekErr := sf.Seek(0, io.SeekStart)
		if seekErr != nil {
			t.Fatalf("seek start: %v", seekErr)
		}
		_, err = gzip.NewReader(sf)
		if err == nil {
			t.Fatalf("expected naive restart on punched source to fail with gzip invalid header")
		}

		// Verification without checkpoint must report ErrUnrecoverableSparseSource.
		_, err = ValidateSourceForRestart(sf, "")
		if err != ErrUnrecoverableSparseSource {
			t.Fatalf("expected ErrUnrecoverableSparseSource, got %v", err)
		}
	})
}
