package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

func BenchmarkTLSHandshake_ClassicalX25519(b *testing.B) {
	benchmarkTLSHandshake(b, []tls.CurveID{tls.X25519})
}

func BenchmarkTLSHandshake_HybridX25519MLKEM768(b *testing.B) {
	benchmarkTLSHandshake(b, []tls.CurveID{tls.X25519MLKEM768, tls.X25519})
}

func BenchmarkApplyPostQuantumTLSAuto(b *testing.B) {
	base := &tls.Config{CurvePreferences: []tls.CurveID{tls.X25519}}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ApplyPostQuantumTLS(base, PostQuantumTLSAuto); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkTLSHandshake(b *testing.B, curves []tls.CurveID) {
	cert := testCertificate(b)
	serverCfg := &tls.Config{
		Certificates:           []tls.Certificate{cert},
		CurvePreferences:       curves,
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		SessionTicketsDisabled: true,
	}
	clientCfg := &tls.Config{
		InsecureSkipVerify: true, // test-only local self-signed certificate
		CurvePreferences:   curves,
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					errCh <- err
					return
				}
			}
			go func(conn net.Conn) {
				tlsConn := tls.Server(conn, serverCfg)
				if err := tlsConn.Handshake(); err != nil {
					errCh <- err
				}
				_ = tlsConn.Close()
			}(conn)
		}
	}()
	defer close(done)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client, err := tls.Dial("tcp", listener.Addr().String(), clientCfg)
		if err != nil {
			b.Fatal(err)
		}
		_ = client.Close()
		select {
		case err := <-errCh:
			b.Fatal(err)
		default:
		}
	}
}

func testCertificate(tb testing.TB) tls.Certificate {
	tb.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		DNSNames:  []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		tb.Fatalf("create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		tb.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		tb.Fatalf("key pair: %v", err)
	}
	return cert
}
