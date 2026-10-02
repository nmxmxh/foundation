// Package httptransport supplies the shared outbound HTTP transport used by the
// connector drivers.
//
// It exists because a nil Transport on http.Client falls back to
// http.DefaultTransport, whose MaxIdleConnsPerHost is DefaultMaxIdleConnsPerHost
// (2). A driver that probes health on an interval and also serves Call traffic
// therefore discarded every connection past the second concurrent one and paid a
// TCP (and TLS) handshake for it. One shared, explicitly sized transport keeps
// those sockets warm across drivers.
package httptransport

import (
	"net/http"
	"sync"
	"time"
)

const (
	// DefaultMaxIdleConns bounds the whole idle pool. It is generous because the
	// pool is shared: a driver may talk to several upstreams.
	DefaultMaxIdleConns = 128
	// DefaultMaxIdleConnsPerHost is the per-upstream pool. The Go default of 2
	// is the bottleneck this package exists to remove.
	DefaultMaxIdleConnsPerHost = 32
	// DefaultIdleConnTimeout retires sockets a peer may have closed.
	DefaultIdleConnTimeout = 90 * time.Second
	// DefaultTLSHandshakeTimeout bounds a stalled handshake.
	DefaultTLSHandshakeTimeout = 10 * time.Second
	// DefaultExpectContinueTimeout bounds the 100-continue wait.
	DefaultExpectContinueTimeout = 1 * time.Second
	// DefaultResponseHeaderTimeout bounds a server that accepts but never
	// answers. A zero value means no limit, which lets one peer pin a
	// connection indefinitely.
	DefaultResponseHeaderTimeout = 30 * time.Second
	// DefaultMaxConnsPerHost caps concurrent dials to one upstream.
	DefaultMaxConnsPerHost = 64
)

var shared *http.Transport

// Shared returns the process-wide transport. It is safe for concurrent use and
// reuses idle connections across every driver that takes it.
func Shared() *http.Transport {
	return sharedTransport()
}

func sharedTransport() *http.Transport {
	sharedOnce.Do(func() {
		base, _ := http.DefaultTransport.(*http.Transport)
		shared = newTransport(base)
	})
	return shared
}

// New returns a transport derived from a template. A nil template uses the Go
// defaults as a base. Dial, TLS, and proxy settings on the template are kept.
func New(template *http.Transport) *http.Transport {
	return newTransport(template)
}

func newTransport(template *http.Transport) *http.Transport {
	t := &http.Transport{}
	if template != nil {
		t = template.Clone()
	}
	t.MaxIdleConns = DefaultMaxIdleConns
	t.MaxIdleConnsPerHost = DefaultMaxIdleConnsPerHost
	t.IdleConnTimeout = DefaultIdleConnTimeout
	t.TLSHandshakeTimeout = DefaultTLSHandshakeTimeout
	t.ExpectContinueTimeout = DefaultExpectContinueTimeout
	t.ResponseHeaderTimeout = DefaultResponseHeaderTimeout
	t.MaxConnsPerHost = DefaultMaxConnsPerHost
	// Force HTTP/2. A hand-built Transport does not negotiate h2 on its own,
	// so an explicit field is what actually upgrades the connection.
	t.ForceAttemptHTTP2 = true
	return t
}

// NewClient returns a client over the shared transport with the given timeout.
// A timeout of zero leaves the deadline to the caller context.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: Shared(), Timeout: timeout}
}

var sharedOnce sync.Once
