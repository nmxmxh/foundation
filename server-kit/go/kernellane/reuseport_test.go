package kernellane

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestReusePortListenerCarriesDataRegardlessOfSupport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lc := ReusePortListenConfig()
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	want := []byte("foundation-reuseport")
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

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, want)
	}
	if err := <-errc; err != nil {
		t.Fatalf("server write: %v", err)
	}
}

// TestReusePortDistributesAcrossListeners is the accelerator's actual claim: on
// a supporting host, two independent listeners bind the same port and both can
// serve. Where the platform does not support it the test asserts the honest
// fallback instead — the second bind fails and the probe says so.

func TestReusePortDistributesAcrossListeners(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lc := ReusePortListenConfig()
	first, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer func() { _ = first.Close() }()

	second, err := lc.Listen(ctx, "tcp", first.Addr().String())
	supported := ReusePortSupported(ctx)
	if err != nil {
		if supported {
			t.Fatalf("ReusePortSupported reports true but second bind failed: %v", err)
		}
		t.Logf("SO_REUSEPORT unsupported on this host; single-listener fallback holds")
		return
	}
	defer func() { _ = second.Close() }()

	if !supported {
		t.Fatal("second bind succeeded but ReusePortSupported reports false")
	}

	// The portable claim is that two listeners share one port and every
	// connection is served — NOT that the kernel distributes them evenly.
	// Linux 3.9+ hashes new connections across the bound sockets; Darwin hands
	// them all to the most recent binder. Asserting fairness here would encode
	// Linux behaviour into a test that also runs on macOS, so both listeners
	// accept in a loop and only the total is checked.
	const dials = 4
	var wg sync.WaitGroup
	served := make(chan struct{}, dials)
	for _, ln := range []net.Listener{first, second} {
		wg.Add(1)
		go func(ln net.Listener) {
			defer wg.Done()
			for {
				conn, err := ln.Accept()
				if err != nil {
					return // listener closed; the deferred Close ends the loop.
				}
				_ = conn.Close()
				served <- struct{}{}
			}
		}(ln)
	}

	for i := range dials {
		conn, err := net.Dial("tcp", first.Addr().String())
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		_ = conn.Close()
	}

	for i := range dials {
		select {
		case <-served:
		case <-ctx.Done():
			t.Fatalf("only %d of %d connections were served across the shared port", i, dials)
		}
	}

	// Closing both listeners releases the accept loops.
	_ = first.Close()
	_ = second.Close()
	wg.Wait()
}

func TestReusePortSupportedIsCachedAndNonFatal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := ReusePortSupported(ctx)
	if second := ReusePortSupported(ctx); second != first {
		t.Fatalf("ReusePortSupported not stable: %v then %v", first, second)
	}
	t.Logf("SO_REUSEPORT honoured on this host: %v", first)
}

func TestProbeReusePortWithCancelledContextIsFalse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if probeReusePort(ctx) {
		t.Fatal("probe with a cancelled context must report false")
	}
}
