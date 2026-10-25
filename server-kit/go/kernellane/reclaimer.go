package kernellane

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Standard lookback window constants for common compression algorithms.
const (
	// LookbackWindowDeflate is the 32 KiB sliding dictionary window for DEFLATE and Gzip.
	LookbackWindowDeflate int64 = 32 * 1024

	// LookbackWindowBrotli is the 16 MiB sliding dictionary window for Brotli.
	LookbackWindowBrotli int64 = 16 * 1024 * 1024

	// LookbackWindowZstd is the standard 8 MiB window for Zstandard.
	LookbackWindowZstd int64 = 8 * 1024 * 1024

	// DefaultMinPunchInterval is the default batch threshold before issuing a punch syscall.
	DefaultMinPunchInterval int64 = 64 * 1024
)

var (
	// ErrReclaimerAborted reports an operation on an aborted reclaimer.
	ErrReclaimerAborted = errors.New("kernellane: reclaimer aborted")

	// ErrUnrecoverableSparseSource reports a source file with a punched prefix and no checkpoint.
	ErrUnrecoverableSparseSource = errors.New("kernellane: source file has punched prefix without resume checkpoint")

	// ErrLookbackViolation reports an attempt to punch into the protected lookback window.
	ErrLookbackViolation = errors.New("kernellane: punch offset violates lookback window")
)

// ReclaimWatermark records progress for crash recovery and restart validation.
type ReclaimWatermark struct {
	SourceTotalSize     int64  `json:"source_total_size"`
	SourceReadOffset    int64  `json:"source_read_offset"`
	SourcePunchedOffset int64  `json:"source_punched_offset"`
	DestWrittenOffset   int64  `json:"dest_written_offset"`
	ChecksumHex         string `json:"checksum_hex,omitempty"`
	Aborted             bool   `json:"aborted"`
}

// ReclaimOptions defines safety controls for in-place streaming storage reclamation.
type ReclaimOptions struct {
	// LookbackWindow is the byte distance to protect behind the read position.
	LookbackWindow int64

	// MinPunchInterval is the minimum byte threshold before calling PunchHolePrefix.
	MinPunchInterval int64

	// CheckpointPath is the optional file path for writing JSON watermarks.
	CheckpointPath string
}

// Reclaimer manages two-phase commits and lagged hole punching for streaming operations.
type Reclaimer struct {
	mu                  sync.Mutex
	sourceFile          *os.File
	sourceSize          int64
	lookbackWindow      int64
	minPunchInterval    int64
	checkpointPath      string
	sourceReadOffset    int64
	sourcePunchedOffset int64
	destWrittenOffset   int64
	checksumHex         string
	aborted             bool
	closed              bool
}

// NewReclaimer creates a reclaimer for the provided source file.
func NewReclaimer(sourceFile *os.File, opts ReclaimOptions) (*Reclaimer, error) {
	if sourceFile == nil {
		return nil, errors.New("kernellane: nil source file")
	}
	if opts.LookbackWindow < 0 {
		return nil, errors.New("kernellane: negative lookback window")
	}

	st, err := sourceFile.Stat()
	if err != nil {
		return nil, err
	}

	minInterval := opts.MinPunchInterval
	if minInterval <= 0 {
		minInterval = DefaultMinPunchInterval
	}

	return &Reclaimer{
		sourceFile:       sourceFile,
		sourceSize:       st.Size(),
		lookbackWindow:   opts.LookbackWindow,
		minPunchInterval: minInterval,
		checkpointPath:   opts.CheckpointPath,
	}, nil
}

// Commit records verified write progress and punches reclaimed blocks with lookback lag.
// Call this function only after verifying and syncing destination data.
func (r *Reclaimer) Commit(sourceReadOffset, destWrittenOffset int64, checksumHex string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return false, errors.New("kernellane: reclaimer closed")
	}
	if r.aborted {
		return false, ErrReclaimerAborted
	}
	if sourceReadOffset < r.sourceReadOffset {
		return false, errors.New("kernellane: backwards source read offset")
	}
	if destWrittenOffset < r.destWrittenOffset {
		return false, errors.New("kernellane: backwards destination written offset")
	}

	r.sourceReadOffset = sourceReadOffset
	r.destWrittenOffset = destWrittenOffset
	r.checksumHex = checksumHex

	// Calculate safe punch offset protected by the lookback window.
	safeLimit := sourceReadOffset - r.lookbackWindow
	if safeLimit <= 0 {
		return false, nil
	}

	// Align to BlockSize boundary.
	alignedLimit := safeLimit & BlockMask
	if alignedLimit <= r.sourcePunchedOffset {
		return false, nil
	}

	// Enforce minimum batch interval to prevent syscall thrashing.
	if (alignedLimit - r.sourcePunchedOffset) < r.minPunchInterval {
		return false, nil
	}

	punched, err := PunchHolePrefix(r.sourceFile, alignedLimit)
	if err != nil {
		return false, err
	}

	r.sourcePunchedOffset = alignedLimit

	if r.checkpointPath != "" {
		if err := r.saveCheckpointLocked(); err != nil {
			return punched, err
		}
	}

	return punched, nil
}

// Abort halts the reclaimer, executes cleanup, and prevents further punch actions.
func (r *Reclaimer) Abort(cleanupFn func() error) error {
	r.mu.Lock()
	r.aborted = true
	var cleanupErr error
	if cleanupFn != nil {
		cleanupErr = cleanupFn()
	}
	if r.checkpointPath != "" {
		_ = r.saveCheckpointLocked()
	}
	r.mu.Unlock()
	return cleanupErr
}

// Close finishes reclaimer operations and saves the final checkpoint.
func (r *Reclaimer) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true

	if r.checkpointPath != "" && !r.aborted {
		return r.saveCheckpointLocked()
	}
	return nil
}

// Watermark returns a copy of current progress indicators.
func (r *Reclaimer) Watermark() ReclaimWatermark {
	r.mu.Lock()
	defer r.mu.Unlock()

	return ReclaimWatermark{
		SourceTotalSize:     r.sourceSize,
		SourceReadOffset:    r.sourceReadOffset,
		SourcePunchedOffset: r.sourcePunchedOffset,
		DestWrittenOffset:   r.destWrittenOffset,
		ChecksumHex:         r.checksumHex,
		Aborted:             r.aborted,
	}
}

// saveCheckpointLocked writes the current watermark to disk atomically.
func (r *Reclaimer) saveCheckpointLocked() error {
	wm := ReclaimWatermark{
		SourceTotalSize:     r.sourceSize,
		SourceReadOffset:    r.sourceReadOffset,
		SourcePunchedOffset: r.sourcePunchedOffset,
		DestWrittenOffset:   r.destWrittenOffset,
		ChecksumHex:         r.checksumHex,
		Aborted:             r.aborted,
	}

	data, err := json.MarshalIndent(wm, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(r.checkpointPath)
	tmpFile, err := os.CreateTemp(dir, "reclaim-ckpt-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	_ = tmpFile.Close()

	if err := os.WriteFile(tmpName, data, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	return os.Rename(tmpName, r.checkpointPath)
}

// LoadCheckpoint reads and parses a saved reclaim watermark from disk.
func LoadCheckpoint(path string) (ReclaimWatermark, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is caller-specified checkpoint path.
	if err != nil {
		return ReclaimWatermark{}, err
	}
	var wm ReclaimWatermark
	if err := json.Unmarshal(data, &wm); err != nil {
		return ReclaimWatermark{}, err
	}
	return wm, nil
}

// InspectPunchedPrefix checks whether the first block of the file is punched or zeroed.
func InspectPunchedPrefix(f *os.File) (bool, error) {
	if f == nil {
		return false, errors.New("kernellane: nil file descriptor")
	}

	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	if st.Size() == 0 {
		return false, nil
	}

	checkLen := min(st.Size(), int64(BlockSize))

	buf := make([]byte, checkLen)
	if _, err := f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}

	for _, b := range buf {
		if b != 0x00 {
			return false, nil
		}
	}
	return true, nil
}

// ValidateSourceForRestart verifies that a source file can be resumed safely.
func ValidateSourceForRestart(sourceFile *os.File, checkpointPath string) (ReclaimWatermark, error) {
	isPunched, err := InspectPunchedPrefix(sourceFile)
	if err != nil {
		return ReclaimWatermark{}, err
	}

	if checkpointPath != "" {
		wm, err := LoadCheckpoint(checkpointPath)
		if err == nil {
			if wm.Aborted {
				return wm, ErrReclaimerAborted
			}
			return wm, nil
		}
	}

	if isPunched {
		return ReclaimWatermark{}, ErrUnrecoverableSparseSource
	}

	st, err := sourceFile.Stat()
	if err != nil {
		return ReclaimWatermark{}, err
	}

	return ReclaimWatermark{
		SourceTotalSize:     st.Size(),
		SourceReadOffset:    0,
		SourcePunchedOffset: 0,
		DestWrittenOffset:   0,
	}, nil
}
