package emailx

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

func testRSAKey(t testing.TB) (privPEM, pubPEM string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	privDER := x509.MarshalPKCS1PrivateKey(priv)
	privPEM = string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privDER,
	}))

	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM = string(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubDER,
	}))

	return privPEM, pubPEM
}

func TestSignDKIM(t *testing.T) {
	privPEM, pubPEM := testRSAKey(t)

	rawEmail := []byte("From: sender@example.com\r\nTo: rcpt@example.com\r\nSubject: Test\r\nDate: Tue, 01 Oct 2026 12:00:00 +0000\r\nMessage-ID: <123@example.com>\r\n\r\nHello World\r\n")

	cfg := DKIMConfig{
		Domain:     "example.com",
		Selector:   "sel1",
		PrivateKey: privPEM,
	}

	signed, err := SignDKIM(rawEmail, cfg)
	if err != nil {
		t.Fatalf("failed to sign message with DKIM: %v", err)
	}

	signedStr := string(signed)
	if !strings.HasPrefix(signedStr, "DKIM-Signature:") {
		t.Fatal("expected message to start with DKIM-Signature")
	}
	if !strings.Contains(signedStr, "d=example.com") {
		t.Fatal("missing domain tag in DKIM-Signature")
	}
	if !strings.Contains(signedStr, "s=sel1") {
		t.Fatal("missing selector tag in DKIM-Signature")
	}
	if !strings.Contains(signedStr, "a=rsa-sha256") {
		t.Fatal("missing algorithm tag in DKIM-Signature")
	}

	// Format DNS TXT record with RFC 1035 chunking
	dnsRecord, err := FormatDKIMRecord(pubPEM)
	if err != nil {
		t.Fatalf("failed to format DKIM DNS record: %v", err)
	}
	if !strings.HasPrefix(dnsRecord, "\"v=DKIM1; k=rsa;") {
		t.Fatalf("unexpected DNS record prefix: %s", dnsRecord)
	}
	if !strings.Contains(dnsRecord, "\" \"") {
		t.Fatal("expected chunked quotes for 2048-bit key in DNS record")
	}

	// Bad config tests
	if _, err := SignDKIM(rawEmail, DKIMConfig{}); err == nil {
		t.Fatal("expected error with empty DKIM config")
	}
	if _, err := SignDKIM(rawEmail, DKIMConfig{Domain: "d", Selector: "s", PrivateKey: "invalid"}); err == nil {
		t.Fatal("expected error with invalid PEM key")
	}
}
