package httpserver

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// TLSConfig defines parameters for secure TLS termination.
type TLSConfig struct {
	CertFile                 string
	KeyFile                  string
	Certificates             []tls.Certificate
	GetCertificate           func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	MinVersion               uint16
	MaxVersion               uint16
	SessionTicketsDisabled   bool
	SessionTicketKey         *[32]byte
	PostQuantumMode          security.PostQuantumTLSMode
	ClientAuth               tls.ClientAuthType
	ClientCAs                *x509.CertPool
	NextProtos               []string
	PreferServerCipherSuites bool
}

// SecureCipherSuites provides modern AEAD cipher suites for TLS 1.2 fallback.
var SecureCipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
}

// DefaultSecureTLSConfig returns a hardened base TLS configuration.
func DefaultSecureTLSConfig() *tls.Config {
	cfg := &tls.Config{
		MinVersion:               tls.VersionTLS13,
		CipherSuites:             SecureCipherSuites,
		NextProtos:               []string{"h2", "http/1.1"},
		SessionTicketsDisabled:   false,
		PreferServerCipherSuites: true,
	}

	pqCfg, err := security.ApplyPostQuantumTLS(cfg, security.PostQuantumTLSAuto)
	if err == nil {
		return pqCfg
	}
	return cfg
}

// BuildTLSConfig constructs and validates a crypto/tls Config struct.
func BuildTLSConfig(opts *TLSConfig) (*tls.Config, error) {
	if opts == nil {
		return DefaultSecureTLSConfig(), nil
	}

	minVer := opts.MinVersion
	if minVer == 0 {
		minVer = tls.VersionTLS13
	}
	if minVer < tls.VersionTLS12 {
		return nil, fmt.Errorf("insecure TLS version: minimum allowed version is TLS 1.2")
	}

	base := &tls.Config{
		MinVersion:               minVer,
		MaxVersion:               opts.MaxVersion,
		Certificates:             opts.Certificates,
		GetCertificate:           opts.GetCertificate,
		CipherSuites:             SecureCipherSuites,
		NextProtos:               opts.NextProtos,
		SessionTicketsDisabled:   opts.SessionTicketsDisabled,
		ClientAuth:               opts.ClientAuth,
		ClientCAs:                opts.ClientCAs,
		// #nosec G402
		PreferServerCipherSuites: opts.PreferServerCipherSuites,
	}

	if len(base.NextProtos) == 0 {
		base.NextProtos = []string{"h2", "http/1.1"}
	}

	if opts.SessionTicketKey != nil {
		base.SetSessionTicketKeys([][32]byte{*opts.SessionTicketKey})
	}

	if opts.CertFile != "" && opts.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS keypair: %w", err)
		}
		base.Certificates = append(base.Certificates, cert)
	}

	mode := opts.PostQuantumMode
	if mode == "" {
		mode = security.PostQuantumTLSAuto
	}

	configured, err := security.ApplyPostQuantumTLS(base, mode)
	if err != nil {
		return nil, fmt.Errorf("apply post-quantum TLS: %w", err)
	}

	return configured, nil
}

// HTTP2Config defines connection parameters for HTTP/2 multiplexing.
type HTTP2Config struct {
	MaxConcurrentStreams uint32
	MaxReadFrameSize     uint32
	IdleTimeout          time.Duration
}

// DefaultHTTP2Config returns production defaults for HTTP/2 execution.
func DefaultHTTP2Config() HTTP2Config {
	return HTTP2Config{
		MaxConcurrentStreams: 250,
		MaxReadFrameSize:     1 << 20, // 1 MB
		IdleTimeout:          120 * time.Second,
	}
}
