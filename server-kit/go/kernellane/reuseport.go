package kernellane

import (
	"context"
	"net"
	"sync"
)

// ReusePortListenConfig returns a net.ListenConfig that requests SO_REUSEPORT,
// allowing several independent listeners to bind the same address so the kernel
// distributes incoming connections across them.
//
// This removes a layer rather than tuning one: without it, a multi-core service
// funnels every accept through a single listener and its accept queue, and the
// distribution work lands in userspace. With it, the kernel does the fan-out.
//
// Where the platform does not support SO_REUSEPORT the config is an ordinary
// ListenConfig, so a single listener still binds successfully and behaviour is
// preserved — only the second concurrent bind would fail, which is exactly what
// ReusePortSupported probes for. Callers that need certainty should consult
// ReusePortSupported before starting more than one acceptor.
//
// Note on semantics: Linux (3.9+) load-balances new connections across the
// bound sockets. Darwin and the BSDs accept the option but distribute
// differently, so treat cross-platform distribution fairness as unspecified and
// benchmark before relying on it.
func ReusePortListenConfig() net.ListenConfig {
	return net.ListenConfig{Control: reusePortControl}
}

var reusePortProbe struct {
	once sync.Once
	ok   bool
}

// ReusePortSupported reports, with a cached one-shot probe, whether this host
// really allows two listeners on the same address. It binds a loopback port
// twice; anything other than two successful binds reports false. It never
// errors fatally.
func ReusePortSupported(ctx context.Context) bool {
	reusePortProbe.once.Do(func() { reusePortProbe.ok = probeReusePort(ctx) })
	return reusePortProbe.ok
}

func probeReusePort(ctx context.Context) bool {
	if !reusePortAvailable {
		return false
	}
	// Listening on a literal address performs no resolution, so ListenConfig
	// would happily bind under a cancelled context. Honour cancellation
	// explicitly rather than appearing to respect a context we never consult.
	if ctx.Err() != nil {
		return false
	}
	lc := ReusePortListenConfig()

	first, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return false
	}
	defer func() { _ = first.Close() }()

	// The real question is not "did setsockopt return 0" but "can a second
	// listener actually bind the same port". Probe the behaviour, not the flag.
	second, err := lc.Listen(ctx, "tcp", first.Addr().String())
	if err != nil {
		return false
	}
	_ = second.Close()
	return true
}
