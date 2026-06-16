package kernellane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCopyFileMatchesSourceAcrossSizes is the zero-copy parity test: whichever
// path runs (kernel copy_file_range on Linux, portable io.Copy elsewhere), the
// destination bytes must be byte-identical to the source, and the reported
// zeroCopy flag must agree with the capability probe.
func TestCopyFileMatchesSourceAcrossSizes(t *testing.T) {
	for _, size := range []int{0, 1, 7, 4096, 100_000} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(i*31 + 7)
		}
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "src.bin")
		dstPath := filepath.Join(dir, "dst.bin")
		if err := os.WriteFile(srcPath, payload, 0o600); err != nil {
			t.Fatalf("write src: %v", err)
		}
		src, err := os.Open(srcPath)
		if err != nil {
			t.Fatalf("open src: %v", err)
		}
		dst, err := os.Create(dstPath)
		if err != nil {
			_ = src.Close()
			t.Fatalf("create dst: %v", err)
		}

		n, zc, err := CopyFile(dst, src, int64(size))
		_ = src.Close()
		_ = dst.Close()
		if err != nil {
			t.Fatalf("size=%d CopyFile error: %v", size, err)
		}
		if n != int64(size) {
			t.Fatalf("size=%d copied %d bytes", size, n)
		}
		if size > 0 && zc != ZeroCopyFileSupported() {
			t.Fatalf("size=%d zeroCopy=%v but ZeroCopyFileSupported=%v", size, zc, ZeroCopyFileSupported())
		}
		got, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatalf("read dst: %v", err)
		}
		if sha256.Sum256(got) != sha256.Sum256(payload) {
			t.Fatalf("size=%d destination bytes differ from source", size)
		}
	}
}

func TestCopyFileRejectsNegativeSize(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "f.bin"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, _, err := CopyFile(f, f, -1); err == nil {
		t.Fatal("negative size should error")
	}
}

func TestCopyFileFallbackSurfacesWriteError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "src.bin"), []byte("payload"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	src, err := os.Open(filepath.Join(dir, "src.bin"))
	if err != nil {
		t.Fatalf("open src: %v", err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.Create(filepath.Join(dir, "dst.bin"))
	if err != nil {
		t.Fatalf("create dst: %v", err)
	}
	_ = dst.Close() // closing first forces the copy write to fail.

	if _, _, err := CopyFile(dst, src, 7); err == nil {
		t.Fatal("copy into a closed destination should error")
	}
}

func TestProbeMultipathTCPWithCancelledContextIsFalse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before probing so listen/dial fails fast.
	if probeMultipathTCP(ctx) {
		t.Fatal("probe with a cancelled context must report false")
	}
}

func TestZeroCopyFileSupportedIsCachedAndConsistent(t *testing.T) {
	first := ZeroCopyFileSupported()
	if second := ZeroCopyFileSupported(); second != first {
		t.Fatalf("ZeroCopyFileSupported not stable: %v then %v", first, second)
	}
	t.Logf("kernel zero-copy supported on this host: %v", first)
}

// TestMultipathConfigsCarryDataRegardlessOfMPTCPSupport proves the MPTCP dialer
// and listener are always usable: data round-trips whether the kernel negotiates
// MPTCP or silently falls back to ordinary TCP.
func TestMultipathConfigsCarryDataRegardlessOfMPTCPSupport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lc := MultipathListenConfig()
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	want := []byte("foundation-kernellane")
	errc := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errc <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_, err = conn.Write(want)
		errc <- err
	}()

	conn, err := MultipathDialer().DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, want)
	}
	if err := <-errc; err != nil {
		t.Fatalf("server write: %v", err)
	}

	// MultipathTCP() must be queryable on the concrete connection type.
	if tc, ok := conn.(*net.TCPConn); ok {
		if _, err := tc.MultipathTCP(); err != nil {
			t.Fatalf("MultipathTCP query: %v", err)
		}
	}
}

func TestMultipathTCPSupportedIsCachedAndNonFatal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := MultipathTCPSupported(ctx)
	if second := MultipathTCPSupported(ctx); second != first {
		t.Fatalf("MultipathTCPSupported not stable: %v then %v", first, second)
	}
	t.Logf("MPTCP negotiated on this host: %v", first)
}
