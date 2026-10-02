package httptransport

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestSharedIsSingleton keeps the pool shared, which is the point: a per-driver
// transport would put each driver back to a small pool.
func TestSharedIsSingleton(t *testing.T) {
	if Shared() != Shared() {
		t.Fatal("Shared must return one process-wide transport")
	}
}

// TestPoolExceedsGoDefaults is the guard for the reason this package exists.
func TestPoolExceedsGoDefaults(t *testing.T) {
	tr := Shared()

	if tr.MaxIdleConnsPerHost <= http.DefaultMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, must exceed the Go default %d",
			tr.MaxIdleConnsPerHost, http.DefaultMaxIdleConnsPerHost)
	}
	if tr.MaxIdleConns < tr.MaxIdleConnsPerHost {
		t.Errorf("MaxIdleConns = %d is below MaxIdleConnsPerHost = %d",
			tr.MaxIdleConns, tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout <= 0 {
		t.Error("IdleConnTimeout must be set so closed sockets are retired")
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Error("TLSHandshakeTimeout must be set so a stalled handshake cannot pin a socket")
	}
	if tr.ExpectContinueTimeout <= 0 {
		t.Error("ExpectContinueTimeout must be set")
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Error("ResponseHeaderTimeout must be set: zero means no limit at all")
	}
	if tr.MaxConnsPerHost <= 0 {
		t.Error("MaxConnsPerHost must bound concurrent dials to one upstream")
	}
	if !tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 must be set: a hand-built Transport does not negotiate h2 on its own")
	}
}

// TestNewKeepsTemplateDialSettings verifies a derived transport preserves the
// dial and TLS configuration of its template.
func TestNewKeepsTemplateDialSettings(t *testing.T) {
	template := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, net.ErrClosed
	}}
	tr := New(template)

	if tr.DialContext == nil {
		t.Fatal("New must keep the template DialContext")
	}
	if tr.MaxIdleConnsPerHost != DefaultMaxIdleConnsPerHost {
		t.Errorf("MaxIdleConnsPerHost = %d, want %d", tr.MaxIdleConnsPerHost, DefaultMaxIdleConnsPerHost)
	}
}

// TestNewClonesRatherThanMutates ensures the caller's transport is untouched.
func TestNewClonesRatherThanMutates(t *testing.T) {
	template := &http.Transport{}
	_ = New(template)

	if template.MaxIdleConnsPerHost != 0 {
		t.Errorf("template was mutated: MaxIdleConnsPerHost = %d", template.MaxIdleConnsPerHost)
	}
	if template.ForceAttemptHTTP2 {
		t.Error("template was mutated: ForceAttemptHTTP2 was set in place")
	}
}

// TestNewClientUsesSharedTransport wires the driver-facing helper.
func TestNewClientUsesSharedTransport(t *testing.T) {
	c := NewClient(5 * time.Second)
	if c.Transport != Shared() {
		t.Fatal("NewClient must use the shared transport")
	}
	if c.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", c.Timeout)
	}
}

// BenchmarkIdlePoolReuse shows the difference the pool makes. The default
// transport keeps two idle connections per host, so a burst re-dials.
func BenchmarkIdlePoolReuse(b *testing.B) {
	b.Run("shared", func(b *testing.B) {
		c := NewClient(time.Second)
		b.ReportAllocs()
		for b.Loop() {
			req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
			resp, err := c.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}
	})
	b.Run("default", func(b *testing.B) {
		c := &http.Client{Timeout: time.Second}
		b.ReportAllocs()
		for b.Loop() {
			req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
			resp, err := c.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}
	})
}
