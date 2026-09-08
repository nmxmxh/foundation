package httpserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/profiling"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

// writeCertPair writes a self-signed keypair and returns its file paths, so a
// Config can be exercised through the CertFile/KeyFile lane New actually uses.
func writeCertPair(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()
	certFile = filepath.Join(dir, "server.crt")
	keyFile = filepath.Join(dir, "server.key")

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() err=%v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate() err=%v", err)
	}
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() err=%v", err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write cert err=%v", err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}), 0o600); err != nil {
		t.Fatalf("write key err=%v", err)
	}
	return certFile, keyFile
}

// Every transport tunable on Config is meant to override the built-in default.
// A field that silently keeps the default is the failure this pins down: the
// operator sets a timeout, the server ignores it, and nothing says so.
func TestNewAppliesConfiguredTransportTunables(t *testing.T) {
	certFile, keyFile := writeCertPair(t)
	rates := profiling.SampledRates()
	server := New(&Config{
		Port:                 8080,
		EnableProfiling:      true,
		ProfilingRates:       &rates,
		TLSEnabled:           true,
		CertFile:             certFile,
		KeyFile:              keyFile,
		ReadHeaderTimeout:    3 * time.Second,
		ReadTimeout:          7 * time.Second,
		WriteTimeout:         11 * time.Second,
		IdleTimeout:          13 * time.Second,
		MaxConcurrentStreams: 42,
	}, nil)

	if server.readHeaderTimeout != 3*time.Second || server.readTimeout != 7*time.Second {
		t.Fatalf("read timeouts = %v/%v", server.readHeaderTimeout, server.readTimeout)
	}
	if server.writeTimeout != 11*time.Second || server.idleTimeout != 13*time.Second {
		t.Fatalf("write/idle timeouts = %v/%v", server.writeTimeout, server.idleTimeout)
	}
	if server.maxConcurrentStreams != 42 {
		t.Fatalf("maxConcurrentStreams = %d, want 42", server.maxConcurrentStreams)
	}
	if server.tlsConfig == nil || len(server.tlsConfig.Certificates) == 0 {
		t.Fatal("TLSEnabled with a keypair should build a tls.Config carrying the certificate")
	}
}

// An unloadable keypair must not leave the server holding a half-built TLS
// config: it logs and falls through, so Serve later fails loudly on a nil
// config rather than serving with an empty certificate list.
func TestNewSkipsTLSConfigWhenKeypairIsUnloadable(t *testing.T) {
	server := New(&Config{
		TLSEnabled: true,
		CertFile:   filepath.Join(t.TempDir(), "absent.crt"),
		KeyFile:    filepath.Join(t.TempDir(), "absent.key"),
	}, nil)
	if server.tlsConfig != nil {
		t.Fatalf("tlsConfig = %+v, want nil after a failed keypair load", server.tlsConfig)
	}
}

// An explicit tls.Config wins over the file lane without being rebuilt.
func TestNewPrefersExplicitTLSConfig(t *testing.T) {
	explicit := &tls.Config{MinVersion: tls.VersionTLS13}
	server := New(&Config{TLSEnabled: true, TLSConfig: explicit}, nil)
	if server.tlsConfig != explicit {
		t.Fatal("an explicit TLSConfig should be used as given")
	}
}

// Non-positive rate-limit inputs are normalized rather than accepted: a zero
// window or zero request budget would make the limiter divide by nothing or
// reject every request.
func TestConfigureRateLimitNormalizesAndTakesClient(t *testing.T) {
	server := New(&Config{}, nil)
	client := rediskit.NewMemoryClient("httpservertest")

	server.ConfigureRateLimit(true, 0, 0, client)
	if server.apiRateLimitRequests != 1 {
		t.Fatalf("apiRateLimitRequests = %d, want 1", server.apiRateLimitRequests)
	}
	if server.apiRateLimitWindow != time.Minute {
		t.Fatalf("apiRateLimitWindow = %v, want 1m", server.apiRateLimitWindow)
	}
	if server.apiRedisClient == nil {
		t.Fatal("apiRedisClient should be set from the variadic argument")
	}

	server.ConfigureRateLimit(false, 25, 5*time.Second)
	if server.apiRateLimitEnabled || server.apiRateLimitRequests != 25 || server.apiRateLimitWindow != 5*time.Second {
		t.Fatalf("rate limit = %v/%d/%v", server.apiRateLimitEnabled, server.apiRateLimitRequests, server.apiRateLimitWindow)
	}
}

// The allowset is lazily created, so the method works on a server that never
// went through New (as an embedded or zero-value Server does).
func TestAddUnauthenticatedWSEventInitializesAllowset(t *testing.T) {
	var server Server
	server.AddUnauthenticatedWSEvent("identity:hello:v1:requested")
	if _, ok := server.wsUnauthenticatedAllowset["identity:hello:v1:requested"]; !ok {
		t.Fatal("event should be allowed after AddUnauthenticatedWSEvent")
	}

	fromNew := New(&Config{}, nil)
	fromNew.AddUnauthenticatedWSEvent("identity:hello:v1:requested")
	if len(fromNew.wsUnauthenticatedAllowset) != 2 {
		t.Fatalf("allowset size = %d, want the seeded ping plus the added event", len(fromNew.wsUnauthenticatedAllowset))
	}
}

func TestDefaultHTTP2Config(t *testing.T) {
	cfg := DefaultHTTP2Config()
	if cfg.MaxConcurrentStreams != 250 {
		t.Fatalf("MaxConcurrentStreams = %d, want 250", cfg.MaxConcurrentStreams)
	}
	if cfg.MaxReadFrameSize != 1<<20 {
		t.Fatalf("MaxReadFrameSize = %d, want 1MB", cfg.MaxReadFrameSize)
	}
	if cfg.IdleTimeout != 120*time.Second {
		t.Fatalf("IdleTimeout = %v, want 120s", cfg.IdleTimeout)
	}
}

// failingResponseWriter refuses every write, standing in for a client that
// disconnected mid-response.
type failingResponseWriter struct {
	header http.Header
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *failingResponseWriter) Write([]byte) (int, error) { return 0, errors.New("connection reset") }
func (w *failingResponseWriter) WriteHeader(int)           {}

// A response that cannot be written is logged, not panicked on, and a nil
// logger is tolerated — writeJSON runs on paths where the server may not have
// one.
func TestWriteJSONToleratesWriteFailureAndNilLogger(t *testing.T) {
	server := New(&Config{}, nil)
	writeJSON(server.log, &failingResponseWriter{}, map[string]string{"status": "ok"})
	writeJSON(nil, &failingResponseWriter{}, map[string]string{"status": "ok"})
}

// Serve wraps the listener in TLS when one is configured, and returns cleanly
// when the context ends — the graceful-shutdown contract callers rely on to
// distinguish "we stopped" from "we failed".
func TestServeWrapsListenerInTLSAndStopsOnContext(t *testing.T) {
	cert, pool := generateTestCertificate(t)
	server := New(&Config{TLSConfig: &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
	}}, nil)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener, ctx) }()

	// Prove the socket is actually accepting TLS before shutting it down, so
	// the test cannot pass by racing past a listener that never came up.
	// Verified against the fixture's own CA rather than skipping verification,
	// so the dial proves the server presented the configured certificate.
	conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
	})
	if err != nil {
		cancel()
		t.Fatalf("tls.Dial() err=%v", err)
	}
	_ = conn.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() err=%v, want nil on context cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve() did not return after context cancellation")
	}
}

// A listener that is already closed surfaces as a server error rather than a
// silent return, so a supervisor restarts instead of assuming a clean stop.
func TestServeReportsListenerFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() err=%v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() err=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := New(&Config{}, nil).Serve(listener, ctx); err == nil {
		t.Fatal("Serve(closed listener) err=nil, want a server error")
	}
}

// Run owns its listener, so a port it cannot bind is reported as a wrapped
// listen failure instead of a nil return.
func TestRunReportsUnusableListenAddress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Out of the 16-bit port range: net.Listen rejects it without touching the
	// network, so the test needs no privileged or contended port.
	err := New(&Config{Port: 99999}, nil).Run(ctx)
	if err == nil {
		t.Fatal("Run() err=nil, want a listen failure")
	}
	if !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("Run() err=%v, want it wrapped with the address it tried", err)
	}
}
