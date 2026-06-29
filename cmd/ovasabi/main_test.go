package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordedRun struct {
	name string
	args []string
	dir  string
}

type recordingRunner struct {
	run recordedRun
	err error
}

func (r *recordingRunner) Run(_ context.Context, name string, args []string, dir string, _ []string, _ io.Writer, _ io.Writer) error {
	r.run = recordedRun{name: name, args: append([]string(nil), args...), dir: dir}
	return r.err
}

func TestScriptProfile(t *testing.T) {
	cases := map[string]string{
		"core":        "full",
		"performance": "full",
		"regulated":   "full",
		"lite":        "minimal",
		"backend":     "backend",
	}
	for input, want := range cases {
		got, err := scriptProfile(input)
		if err != nil {
			t.Fatalf("scriptProfile(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("scriptProfile(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestInitWrapsExistingInitializer(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templates", "scaffold.manifest.tsv"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "init.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, runner: runner, client: http.DefaultClient}
	err := a.run(context.Background(), []string{
		"init",
		"--name=trader_os",
		"--profile=performance",
		"--project-dir=/tmp/trader_os_v1",
		"--foundation-dir=" + root,
		"--skip-license",
		"--skip-deps",
		"--dry-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if runner.run.name != filepath.Join(root, "init.sh") {
		t.Fatalf("initializer = %q", runner.run.name)
	}
	want := []string{"trader_os", "full", "--project-dir", "/tmp/trader_os_v1", "--skip-deps", "--dry-run"}
	if !equalStrings(runner.run.args, want) {
		t.Fatalf("args = %#v, want %#v", runner.run.args, want)
	}
}

func TestOnlineLicenseVerification(t *testing.T) {
	var sawAuth bool
	a := app{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		sawAuth = r.Header.Get("Authorization") == "Bearer test-key"
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"active":true}`)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}}
	err := a.verifyLicense(context.Background(), licenseOptions{
		key:       "test-key",
		verifyURL: "https://license.test/verify",
		timeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawAuth {
		t.Fatal("license key was not sent as bearer token")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestOfflineLicenseVerification(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	token := signTestJWT(t, priv, licenseClaims{
		Issuer:   "ovasabi",
		Audience: "ovasabi-cli",
		OrgID:    "org_test",
		Seats:    10,
		Profiles: []string{"performance"},
		Expires:  time.Now().Add(time.Hour).Unix(),
	})
	path := filepath.Join(t.TempDir(), "ovasabi.lic")
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	err = verifyOfflineLicense(licenseOptions{
		file:      path,
		publicKey: pubPEM,
		profile:   "performance",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOfflineLicenseRejectsWrongProfile(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	token := signTestJWT(t, priv, licenseClaims{
		Profiles: []string{"regulated"},
		Expires:  time.Now().Add(time.Hour).Unix(),
	})
	path := filepath.Join(t.TempDir(), "ovasabi.lic")
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	err = verifyOfflineLicense(licenseOptions{
		file: path,
		publicKey: string(pem.EncodeToMemory(&pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: pubDER,
		})),
		profile: "performance",
	})
	if err == nil {
		t.Fatal("expected profile rejection")
	}
}

func signTestJWT(t *testing.T, priv ed25519.PrivateKey, claims licenseClaims) string {
	t.Helper()
	header := map[string]string{"alg": "EdDSA", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	head := base64.RawURLEncoding.EncodeToString(headerJSON)
	body := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signed := head + "." + body
	sig := ed25519.Sign(priv, []byte(signed))
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
