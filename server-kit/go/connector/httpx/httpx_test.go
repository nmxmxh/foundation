package httpx

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/connector"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPXCallAndProbe(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/healthz":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader("OK")),
				}, nil
			case "/api/data":
				h := make(http.Header)
				h.Set("Content-Type", "application/json")
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     h,
					Body:       io.NopCloser(strings.NewReader(`{"success":true}`)),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(strings.NewReader("Not Found")),
				}, nil
			}
		}),
	}

	driver, err := New("https://api.example.com", map[string]any{
		"timeout":        5 * time.Second,
		"max_body_bytes": int64(1024),
		"client":         client,
	})
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}

	// 1. Test Capabilities
	caps, err := driver.Capabilities(context.Background())
	if err != nil || caps.Transport != "http" || !caps.Streaming {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}

	// 2. Test Probe
	h, err := driver.Probe(context.Background())
	if err != nil || h != connector.HealthServing {
		t.Fatalf("probe failed: health=%v, err=%v", h, err)
	}

	// 3. Test Call
	resp, err := driver.Call(context.Background(), connector.Request{
		Operation: "/api/data",
		Method:    http.MethodGet,
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if resp.Status != http.StatusOK || string(resp.Body) != `{"success":true}` {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHTTPXOutboundURLPolicyRejection(t *testing.T) {
	policy := security.OutboundURLPolicy{
		AllowedSchemes:       []string{"https"},
		AllowPrivateNetworks: false,
	}

	driver, err := New("http://127.0.0.1:8080", map[string]any{
		"outbound_policy": &policy,
	})
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}

	// Probe should reject private IP / http scheme
	_, err = driver.Probe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "url safety violation") {
		t.Fatalf("expected url safety violation on probe, got: %v", err)
	}

	// Call should reject private IP / http scheme
	_, err = driver.Call(context.Background(), connector.Request{
		Operation: "/data",
		Method:    http.MethodGet,
	})
	if err == nil || !strings.Contains(err.Error(), "url safety violation") {
		t.Fatalf("expected url safety violation on call, got: %v", err)
	}
}

func TestHTTPXResponseBoundingRejectsOversize(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("A", 2048))),
			}, nil
		}),
	}

	driver, err := New("https://api.example.com", map[string]any{
		"max_body_bytes": int64(64),
		"client":         client,
	})
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}

	resp, err := driver.Call(context.Background(), connector.Request{})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("error = %v, want ErrResponseTooLarge", err)
	}
	if len(resp.Body) != 0 {
		t.Fatalf("oversize response must not return a partial body, got %d bytes", len(resp.Body))
	}
}

func TestHTTPXResponseBoundingAllowsExactLimit(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("A", 64))),
			}, nil
		}),
	}

	driver, err := New("https://api.example.com", map[string]any{
		"max_body_bytes": int64(64),
		"client":         client,
	})
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}

	resp, err := driver.Call(context.Background(), connector.Request{})
	if err != nil {
		t.Fatalf("exact-limit body must succeed, got %v", err)
	}
	if len(resp.Body) != 64 {
		t.Fatalf("body = %d bytes, want 64", len(resp.Body))
	}
}

func TestHTTPXRedirectRejectsPrivateTarget(t *testing.T) {
	policy := security.OutboundURLPolicy{}
	d := &Driver{outboundPolicy: &policy}

	redirected, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err != nil {
		t.Fatalf("failed to build redirect request: %v", err)
	}

	if err := d.redirectPolicy()(redirected, nil); !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("error = %v, want ErrRedirectBlocked", err)
	}
}

func TestHTTPXRedirectAllowsPublicTarget(t *testing.T) {
	policy := security.OutboundURLPolicy{
		Resolver: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
	}
	d := &Driver{outboundPolicy: &policy}

	redirected, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, "https://api.partner.example/v2/events", nil)
	if err != nil {
		t.Fatalf("failed to build redirect request: %v", err)
	}

	if err := d.redirectPolicy()(redirected, nil); err != nil {
		t.Fatalf("public redirect target rejected: %v", err)
	}
}

func TestHTTPXRedirectBlocksEndToEnd(t *testing.T) {
	var hops int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		if hops == 1 {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// The host is a name, so the resolver supplies a public IP and the first hop
	// passes. The redirect target is a literal metadata address, which the
	// policy rejects on the second hop.
	policy := security.OutboundURLPolicy{
		AllowedSchemes: []string{"http", "https"},
		AllowedHosts:   []string{"localhost"},
		Resolver: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		},
	}
	driver, err := New(strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), map[string]any{
		"outbound_policy": policy,
	})
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}

	_, err = driver.Call(context.Background(), connector.Request{})
	if !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("error = %v, want ErrRedirectBlocked", err)
	}
	if hops != 1 {
		t.Fatalf("hops = %d, want 1 (redirect must not be followed)", hops)
	}
}

func TestHTTPXRedirectHopLimit(t *testing.T) {
	var hops int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()

	built, err := New(srv.URL, nil)
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}
	driver, ok := built.(*Driver)
	if !ok {
		t.Fatalf("driver type = %T, want *Driver", built)
	}
	if driver.client.CheckRedirect == nil {
		t.Fatal("driver must install a redirect policy when the caller supplies none")
	}

	if _, err := driver.Call(context.Background(), connector.Request{}); !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("error = %v, want ErrRedirectBlocked", err)
	}
	if hops > 10 {
		t.Fatalf("hops = %d, want at most 10", hops)
	}
}

func TestHTTPXPreservesCallerRedirectPolicy(t *testing.T) {
	sentinel := errors.New("caller policy refused")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return sentinel
	}}

	built, err := New("https://api.example.com", map[string]any{"client": client})
	if err != nil {
		t.Fatalf("failed to create driver: %v", err)
	}
	driver, ok := built.(*Driver)
	if !ok {
		t.Fatalf("driver type = %T, want *Driver", built)
	}
	if driver.client.CheckRedirect == nil {
		t.Fatal("driver must keep the caller CheckRedirect")
	}
	if err := driver.client.CheckRedirect(nil, nil); !errors.Is(err, sentinel) {
		t.Fatalf("CheckRedirect = %v, want the caller sentinel", err)
	}
}

// TestHTTPXDefaultClientUsesPooledTransport is the guard for the connection
// pool. A nil Transport falls back to http.DefaultTransport, whose
// MaxIdleConnsPerHost is 2, so probe traffic past the second concurrent request
// re-dialled every time.
func TestHTTPXDefaultClientUsesPooledTransport(t *testing.T) {
	built, err := New("https://api.example.com", nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	driver, ok := built.(*Driver)
	if !ok {
		t.Fatalf("driver type = %T, want *Driver", built)
	}

	tr, ok := driver.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport", driver.client.Transport)
	}
	if tr.MaxIdleConnsPerHost <= http.DefaultMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, must exceed the Go default %d",
			tr.MaxIdleConnsPerHost, http.DefaultMaxIdleConnsPerHost)
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Error("ResponseHeaderTimeout must be set on the driver transport")
	}
	if driver.client.Timeout != defaultCallTimeout {
		t.Errorf("Timeout = %v, want %v", driver.client.Timeout, defaultCallTimeout)
	}
}

// TestHTTPXCallerClientWins keeps the injection point working.
func TestHTTPXCallerClientWins(t *testing.T) {
	sentinel := &http.Client{Timeout: 1234 * time.Millisecond}
	built, err := New("https://api.example.com", map[string]any{"client": sentinel})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	driver := built.(*Driver)
	if driver.client != sentinel {
		t.Fatal("a caller-supplied client must be used as given")
	}
}
