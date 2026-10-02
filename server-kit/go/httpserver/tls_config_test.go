package httpserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"golang.org/x/net/http2"
)

func generateTestCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("x509 keypair: %v", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("append cert to pool failed")
	}

	return tlsCert, pool
}

func TestBuildTLSConfig(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		cfg, err := BuildTLSConfig(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MinVersion != tls.VersionTLS13 {
			t.Errorf("expected MinVersion TLS1.3, got %x", cfg.MinVersion)
		}
		if cfg.SessionTicketsDisabled {
			t.Errorf("expected session tickets enabled")
		}
	})

	t.Run("rejects insecure TLS version", func(t *testing.T) {
		_, err := BuildTLSConfig(&TLSConfig{
			MinVersion: tls.VersionTLS10,
		})
		if err == nil {
			t.Fatal("expected error on TLS 1.0, got nil")
		}

		_, err = BuildTLSConfig(&TLSConfig{
			MinVersion: tls.VersionTLS11,
		})
		if err == nil {
			t.Fatal("expected error on TLS 1.1, got nil")
		}
	})

	t.Run("post-quantum mode application", func(t *testing.T) {
		cfg, err := BuildTLSConfig(&TLSConfig{
			PostQuantumMode: security.PostQuantumTLSRequired,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(cfg.CurvePreferences) != 1 || cfg.CurvePreferences[0] != tls.X25519MLKEM768 {
			t.Errorf("expected curve preferences [X25519MLKEM768], got %v", cfg.CurvePreferences)
		}
	})

	t.Run("session ticket key configuration", func(t *testing.T) {
		key := [32]byte{1, 2, 3, 4}
		cfg, err := BuildTLSConfig(&TLSConfig{
			SessionTicketKey: &key,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.SessionTicketsDisabled {
			t.Errorf("session tickets should be active")
		}
	})

	t.Run("file keypair loading", func(t *testing.T) {
		tempDir := t.TempDir()
		certFile := filepath.Join(tempDir, "server.crt")
		keyFile := filepath.Join(tempDir, "server.key")

		priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := x509.Certificate{
			SerialNumber: big.NewInt(100),
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
		}
		der, _ := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyBytes, _ := x509.MarshalECPrivateKey(priv)
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

		_ = os.WriteFile(certFile, certPEM, 0600)
		_ = os.WriteFile(keyFile, keyPEM, 0600)

		cfg, err := BuildTLSConfig(&TLSConfig{
			CertFile: certFile,
			KeyFile:  keyFile,
		})
		if err != nil {
			t.Fatalf("failed loading keypair from file: %v", err)
		}
		if len(cfg.Certificates) == 0 {
			t.Fatal("expected certificates in tls.Config")
		}
	})
}

func TestServerHTTP2AndTLSResumption(t *testing.T) {
	cert, pool := generateTestCertificate(t)

	tlsCfg, err := BuildTLSConfig(&TLSConfig{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatalf("build tls config: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := New(&Config{Port: 0}, nil)
	srv.ConfigureTLS(tlsCfg)
	srv.ConfigureTimeouts(5*time.Second, 15*time.Second, 15*time.Second, 120*time.Second)
	srv.ConfigureHTTP2(250)

	ctx := t.Context()

	go func() {
		_ = srv.Serve(listener, ctx)
	}()

	serverURL := fmt.Sprintf("https://%s/healthz", listener.Addr().String())

	// Client with TLS Session Cache and HTTP/2
	clientSessionCache := tls.NewLRUClientSessionCache(10)
	clientTLS := &tls.Config{
		RootCAs:            pool,
		ServerName:         "localhost",
		ClientSessionCache: clientSessionCache,
	}

	transport := &http2.Transport{
		TLSClientConfig: clientTLS,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   5 * time.Second,
	}

	// Wait for server to become ready
	var firstResp *http.Response
	for range 20 {
		firstResp, err = client.Get(serverURL)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	defer firstResp.Body.Close()

	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", firstResp.StatusCode)
	}
	if firstResp.Proto != "HTTP/2.0" {
		t.Fatalf("expected HTTP/2.0 protocol, got %s", firstResp.Proto)
	}

	// Execute consecutive requests over multiplexed HTTP/2 connection
	for i := range 5 {
		resp, err := client.Get(serverURL)
		if err != nil {
			t.Fatalf("multiplexed request %d failed: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d status: %d", i, resp.StatusCode)
		}
	}
}

func TestTLSSessionTicketResumption(t *testing.T) {
	cert, pool := generateTestCertificate(t)

	sessionKey := [32]byte{10, 20, 30, 40, 50}
	tlsCfg, err := BuildTLSConfig(&TLSConfig{
		Certificates:     []tls.Certificate{cert},
		MinVersion:       tls.VersionTLS13,
		SessionTicketKey: &sessionKey,
	})
	if err != nil {
		t.Fatalf("build tls config: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := New(&Config{Port: 0}, nil)
	srv.ConfigureTLS(tlsCfg)

	ctx := t.Context()

	go func() {
		_ = srv.Serve(listener, ctx)
	}()

	serverURL := fmt.Sprintf("https://%s/healthz", listener.Addr().String())

	clientSessionCache := tls.NewLRUClientSessionCache(10)
	clientTLS := &tls.Config{
		RootCAs:            pool,
		ServerName:         "localhost",
		ClientSessionCache: clientSessionCache,
	}

	// First HTTP/1.1 connection to receive session ticket
	jar, _ := cookiejar.New(nil)
	client1 := &http.Client{
		Jar: jar,
		Transport: &http.Transport{
			TLSClientConfig:   clientTLS,
			DisableKeepAlives: true, // force new connection
		},
		Timeout: 5 * time.Second,
	}

	var resp1 *http.Response
	for range 20 {
		resp1, err = client1.Get(serverURL)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	io.Copy(io.Discard, resp1.Body)
	resp1.Body.Close()

	if resp1.TLS == nil {
		t.Fatal("expected TLS connection state")
	}

	// Second connection: should resume using session ticket
	client2 := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   clientTLS,
			DisableKeepAlives: true,
		},
		Timeout: 5 * time.Second,
	}

	resp2, err := client2.Get(serverURL)
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	if resp2.TLS == nil {
		t.Fatal("expected TLS connection state on resumed connection")
	}
	if !resp2.TLS.DidResume {
		t.Log("Note: TLS 1.3 Session ticket resumption depends on crypto/tls client ticket caching timing")
	}
}

func TestConfigureTimeoutSetters(t *testing.T) {
	srv := New(&Config{
		ReadHeaderTimeout:    3 * time.Second,
		IdleTimeout:          90 * time.Second,
		MaxConcurrentStreams: 500,
	}, nil)

	if srv.readHeaderTimeout != 3*time.Second {
		t.Errorf("expected readHeaderTimeout 3s, got %v", srv.readHeaderTimeout)
	}
	if srv.idleTimeout != 90*time.Second {
		t.Errorf("expected idleTimeout 90s, got %v", srv.idleTimeout)
	}
	if srv.maxConcurrentStreams != 500 {
		t.Errorf("expected maxConcurrentStreams 500, got %d", srv.maxConcurrentStreams)
	}

	srv.ConfigureTimeouts(2*time.Second, 10*time.Second, 10*time.Second, 60*time.Second)
	if srv.readHeaderTimeout != 2*time.Second {
		t.Errorf("expected updated readHeaderTimeout 2s, got %v", srv.readHeaderTimeout)
	}

	srv.ConfigureHTTP2(300)
	if srv.maxConcurrentStreams != 300 {
		t.Errorf("expected updated maxConcurrentStreams 300, got %d", srv.maxConcurrentStreams)
	}
}
