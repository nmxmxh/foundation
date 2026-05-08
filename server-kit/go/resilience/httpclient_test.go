package resilience

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

func TestHTTPClientEncodesQueryParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("search"); got != "tenant one&role=admin" {
			t.Fatalf("search query = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewHTTPClient(nil, HTTPClientConfig{BaseURL: server.URL})
	resp, err := client.doRequest(context.Background(), Request{
		Method: http.MethodGet,
		Path:   "/v1/search",
		Query:  map[string]string{"search": "tenant one&role=admin"},
	})
	if err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHTTPClientDoConvenienceMethodsAndResponseHelpers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Trace-ID") != "trace_1" {
			t.Fatalf("missing propagated header for %s", r.Method)
		}
		w.Header().Set("X-Reply", r.Method)
		switch r.Method {
		case http.MethodGet:
			if r.URL.Path != "/v1/items" {
				t.Fatalf("GET path = %q", r.URL.Path)
			}
		case http.MethodPost, http.MethodPut:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if body["name"] != "Ada" {
				t.Fatalf("request body = %+v", body)
			}
		case http.MethodDelete:
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"method": r.Method})
	}))
	defer server.Close()

	cfg := DefaultConfig("orders")
	cfg.RetryInitialDelay = time.Millisecond
	cfg.RetryMaxDelay = time.Millisecond
	runtime, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	runtime.RegisterDependency("partner", func(context.Context) error { return nil }, WithCheckInterval(time.Hour))

	client := NewHTTPClient(runtime, HTTPClientConfig{Name: "partner", BaseURL: server.URL})
	headers := map[string]string{"X-Trace-ID": "trace_1"}
	resp, err := client.Get(context.Background(), "/v1/items", headers)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !resp.IsSuccess() || resp.IsClientError() || resp.IsServerError() {
		t.Fatalf("unexpected success helpers for status %d", resp.StatusCode)
	}
	if resp.Headers.Get("X-Reply") != http.MethodGet {
		t.Fatalf("missing response headers")
	}
	var decoded map[string]string
	if err := resp.DecodeJSON(&decoded); err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}
	if decoded["method"] != http.MethodGet {
		t.Fatalf("decoded response = %+v", decoded)
	}

	for name, call := range map[string]func() (*Response, error){
		"post": func() (*Response, error) {
			return client.Post(context.Background(), "/v1/items", map[string]string{"name": "Ada"}, headers)
		},
		"put": func() (*Response, error) {
			return client.Put(context.Background(), "/v1/items/1", map[string]string{"name": "Ada"}, headers)
		},
		"delete": func() (*Response, error) {
			return client.Delete(context.Background(), "/v1/items/1", headers)
		},
	} {
		resp, err := call()
		if err != nil {
			t.Fatalf("%s call error = %v", name, err)
		}
		if !resp.IsSuccess() {
			t.Fatalf("%s status = %d", name, resp.StatusCode)
		}
	}
}

func TestHTTPClientStatusAndDecodeErrors(t *testing.T) {
	clientErr := &Response{StatusCode: http.StatusBadRequest, Body: []byte("{")}
	if clientErr.IsSuccess() || !clientErr.IsClientError() || clientErr.IsServerError() {
		t.Fatalf("unexpected client status helpers")
	}
	if err := clientErr.DecodeJSON(&map[string]any{}); err == nil {
		t.Fatalf("expected decode error")
	}

	serverErr := &Response{StatusCode: http.StatusBadGateway}
	if serverErr.IsSuccess() || serverErr.IsClientError() || !serverErr.IsServerError() {
		t.Fatalf("unexpected server status helpers")
	}
}

func TestHTTPClientDoErrorPaths(t *testing.T) {
	cfg := DefaultConfig("orders")
	cfg.RetryMaxAttempts = 1
	cfg.RetryInitialDelay = time.Millisecond
	cfg.RetryMaxDelay = time.Millisecond
	runtime, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = runtime.Close(context.Background()) }()
	runtime.RegisterDependency("bad-url", func(context.Context) error { return nil }, WithCheckInterval(time.Hour))
	runtime.RegisterDependency("bad-path", func(context.Context) error { return nil }, WithCheckInterval(time.Hour))
	runtime.RegisterDependency("partner", func(context.Context) error { return nil }, WithCheckInterval(time.Hour))

	client := NewHTTPClient(runtime, HTTPClientConfig{Name: "bad-url", BaseURL: "http://[::1"})
	if _, err := client.Get(context.Background(), "/v1/items", nil); err == nil {
		t.Fatalf("expected invalid base URL error")
	}

	client = NewHTTPClient(runtime, HTTPClientConfig{Name: "bad-path", BaseURL: "https://api.example.com"})
	if _, err := client.Do(context.Background(), Request{Method: http.MethodGet, Path: "http://[::1"}); err == nil {
		t.Fatalf("expected invalid request path error")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	client = NewHTTPClient(runtime, HTTPClientConfig{Name: "partner", BaseURL: server.URL})
	if _, err := client.Get(context.Background(), "/v1/items", nil); err == nil {
		t.Fatalf("expected server error")
	}

	runtime.RegisterDependency("degraded", func(context.Context) error { return errors.New("down") }, WithFailureThreshold(1), WithCheckInterval(time.Hour))
	deadline := time.Now().Add(time.Second)
	for !runtime.IsDegraded("degraded") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	client = NewHTTPClient(runtime, HTTPClientConfig{Name: "degraded", BaseURL: server.URL})
	if _, err := client.Get(context.Background(), "/v1/items", nil); err == nil {
		t.Fatalf("expected degraded dependency rejection")
	}
}

func TestHTTPClientRejectsOversizeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("abcdef"))
	}))
	defer server.Close()

	client := NewHTTPClient(nil, HTTPClientConfig{BaseURL: server.URL, MaxResponseBytes: 5})
	_, err := client.doRequest(context.Background(), Request{Method: http.MethodGet, Path: "/data"})
	if err == nil || !strings.Contains(err.Error(), "response body too large") {
		t.Fatalf("error = %v, want response size rejection", err)
	}
}

func TestHTTPClientAppliesOutboundURLPolicy(t *testing.T) {
	policy := security.OutboundURLPolicy{
		AllowedHosts: []string{"api.partner.example"},
		Resolver: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		},
	}
	client := NewHTTPClient(nil, HTTPClientConfig{
		BaseURL:           "https://api.partner.example",
		OutboundURLPolicy: &policy,
	})

	got, err := client.requestURL(context.Background(), Request{Method: http.MethodGet, Path: "/events"})
	if err != nil {
		t.Fatalf("requestURL() error = %v", err)
	}
	if got != "https://api.partner.example/events" {
		t.Fatalf("url = %q", got)
	}

	client.baseURL = "https://169.254.169.254"
	if _, err := client.requestURL(context.Background(), Request{Method: http.MethodGet, Path: "/metadata"}); err == nil {
		t.Fatalf("expected unsafe outbound URL rejection")
	}
}
