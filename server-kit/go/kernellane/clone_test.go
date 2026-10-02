package kernellane

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

// TestCloneFileMatchesSourceAcrossSizes is the clone parity test: whichever lane
// runs — reflink, copy_file_range, or the portable userspace copy — the
// destination bytes must be byte-identical to the source. Lane selection is a
// cost decision, never a semantics decision.
func TestCloneFileMatchesSourceAcrossSizes(t *testing.T) {
	for _, size := range []int{0, 1, 7, 4096, 100_000} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i*17 + 3)
		}
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "src.bin")
		dstPath := filepath.Join(dir, "dst.bin")
		if err := os.WriteFile(srcPath, payload, 0o600); err != nil {
			t.Fatalf("write src: %v", err)
		}

		lane, err := CloneFile(dstPath, srcPath)
		if err != nil {
			t.Fatalf("size=%d CloneFile error: %v", size, err)
		}
		switch lane {
		case CloneLaneReflink, CloneLaneCopyFileRange, CloneLaneUserspace:
		default:
			t.Fatalf("size=%d unknown clone lane %q", size, lane)
		}

		got, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("read dst: %v", err)
		}
		if len(got) != size {
			t.Fatalf("size=%d clone produced %d bytes", size, len(got))
		}
		if sha256.Sum256(got) != sha256.Sum256(payload) {
			t.Fatalf("size=%d clone bytes differ from source (lane %s)", size, lane)
		}
	}
}

// TestCloneFileOverwritesLargerDestination proves the clone truncates: a
// destination left longer than the source would silently corrupt the artifact.

func TestCloneFileOverwritesLargerDestination(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	dstPath := filepath.Join(dir, "dst.bin")
	if err := os.WriteFile(srcPath, []byte("short"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.WriteFile(dstPath, []byte("a much longer previous artifact"), 0o600); err != nil {
		t.Fatalf("write dst: %v", err)
	}

	if _, err := CloneFile(dstPath, srcPath); err != nil {
		t.Fatalf("CloneFile: %v", err)
	}
	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "short" {
		t.Fatalf("clone did not truncate destination: got %q", got)
	}
}

func TestCloneFileMissingSourceErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := CloneFile(filepath.Join(dir, "dst"), filepath.Join(dir, "absent")); err == nil {
		t.Fatal("cloning a missing source should error")
	}
}

func TestCloneFileUncreatableDestinationErrors(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(srcPath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	// A destination inside a non-existent directory cannot be created.
	if _, err := CloneFile(filepath.Join(dir, "missing-parent", "dst"), srcPath); err == nil {
		t.Fatal("cloning into a missing parent directory should error")
	}
}

func TestUserspaceCloneSurfacesSeekAndCopyErrors(t *testing.T) {
	dir := t.TempDir()
	newFile := func(name string) *os.File {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		return f
	}

	// Closed source: the source seek fails first.
	src := newFile("src.bin")
	dst := newFile("dst.bin")
	_ = src.Close()
	if _, err := userspaceClone(dst, src); err == nil {
		t.Fatal("userspace clone from a closed source should error")
	}
	_ = dst.Close()

	// Closed destination with an open source: the destination seek fails.
	src2 := newFile("src2.bin")
	defer func() { _ = src2.Close() }()
	dst2 := newFile("dst2.bin")
	_ = dst2.Close()
	if _, err := userspaceClone(dst2, src2); err == nil {
		t.Fatal("userspace clone into a closed destination should error")
	}

	// Read-only destination: both seeks succeed and the copy write fails, which
	// is a different branch from either seek error.
	src3 := newFile("src3.bin")
	defer func() { _ = src3.Close() }()
	if _, err := src3.WriteString("payload"); err != nil {
		t.Fatalf("write src3: %v", err)
	}
	roPath := filepath.Join(dir, "readonly.bin")
	if err := os.WriteFile(roPath, nil, 0o600); err != nil {
		t.Fatalf("create readonly: %v", err)
	}
	ro, err := os.Open(roPath) // opened O_RDONLY: seekable, not writable.
	if err != nil {
		t.Fatalf("open readonly: %v", err)
	}
	defer func() { _ = ro.Close() }()
	if _, err := userspaceClone(ro, src3); err == nil {
		t.Fatal("userspace clone into a read-only destination should error")
	}
}

// TestCloneFileSurfacesFallbackCopyError drives the same failure through the
// public API: a directory opens successfully but cannot be read as a stream, so
// the error must propagate out of CloneFile rather than be swallowed by the
// lane ladder.

func TestCloneFileSurfacesFallbackCopyError(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "a-directory")
	if err := os.Mkdir(srcDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := CloneFile(filepath.Join(dir, "dst.bin"), srcDir); err == nil {
		t.Fatal("cloning a directory as a file should error")
	}
}

// TestProbeCloneLaneDegradesWhenUnusable proves the capability probe honours the
// package promise: when the filesystem cannot host the probe at all, it reports
// the portable lane instead of failing. A probe that panicked or errored here
// would turn an optional accelerator into a startup dependency.

func TestProbeCloneLaneDegradesWhenUnusable(t *testing.T) {
	unusable := filepath.Join(t.TempDir(), "does-not-exist")
	if lane := probeCloneLaneIn(unusable); lane != CloneLaneUserspace {
		t.Fatalf("probe on an unusable base should report %q, got %q", CloneLaneUserspace, lane)
	}
}

func TestBestCloneLaneIsCachedAndKnown(t *testing.T) {
	first := BestCloneLane()
	if second := BestCloneLane(); second != first {
		t.Fatalf("BestCloneLane not stable: %q then %q", first, second)
	}
	switch first {
	case CloneLaneReflink, CloneLaneCopyFileRange, CloneLaneUserspace:
	default:
		t.Fatalf("unknown best clone lane %q", first)
	}
	t.Logf("best clone lane on this host: %s", first)
}

// TestReusePortListenerCarriesDataRegardlessOfSupport proves the listener is
// always usable: a single acceptor binds and round-trips data whether or not the
// platform honours SO_REUSEPORT.
