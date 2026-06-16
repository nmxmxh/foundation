package kernellane

import (
	"context"
	"net"
	"sync"
)

// MultipathDialer returns a *net.Dialer that requests Multipath TCP. Where the
// OS does not support MPTCP the dial transparently uses ordinary TCP, so the
// dialer is always safe to use.
func MultipathDialer() *net.Dialer {
	d := &net.Dialer{}
	d.SetMultipathTCP(true)
	return d
}

// MultipathListenConfig returns a net.ListenConfig that requests Multipath TCP,
// falling back to ordinary TCP where unsupported.
func MultipathListenConfig() net.ListenConfig {
	lc := net.ListenConfig{}
	lc.SetMultipathTCP(true)
	return lc
}

var mptcpProbe struct {
	once sync.Once
	ok   bool
}

// MultipathTCPSupported reports, with a cached one-shot probe, whether this host
// actually negotiates MPTCP. It establishes a loopback connection with MPTCP
// requested on both ends and checks whether the kernel really used it; a plain
// TCP fallback or any failure reports false. It never errors fatally.
func MultipathTCPSupported(ctx context.Context) bool {
	mptcpProbe.once.Do(func() { mptcpProbe.ok = probeMultipathTCP(ctx) })
	return mptcpProbe.ok
}

func probeMultipathTCP(ctx context.Context) bool {
	lc := MultipathListenConfig()
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	defer func() { _ = ln.Close() }()

	serverUsed := make(chan bool, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverUsed <- false
			return
		}
		defer func() { _ = conn.Close() }()
		if tc, ok := conn.(*net.TCPConn); ok {
			used, _ := tc.MultipathTCP()
			serverUsed <- used
			return
		}
		serverUsed <- false
	}()

	conn, err := MultipathDialer().DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()

	dialedUsed := false
	if tc, ok := conn.(*net.TCPConn); ok {
		dialedUsed, _ = tc.MultipathTCP()
	}

	select {
	case used := <-serverUsed:
		return used && dialedUsed
	case <-ctx.Done():
		return false
	}
}
