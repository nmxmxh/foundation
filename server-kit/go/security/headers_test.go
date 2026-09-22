package security

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSecurityHeadersAllocationBudget(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, scheme := range []string{"http", "https"} {
		req := httptest.NewRequest(http.MethodGet, scheme+"://example.com/", nil)
		response := httptest.NewRecorder()
		budget := float64(1)
		if scheme == "https" {
			budget = 2
		}
		allocations := testing.AllocsPerRun(100, func() { handler.ServeHTTP(response, req) })
		if allocations > budget {
			t.Fatalf("%s headers allocate %.0f times, budget %.0f", scheme, allocations, budget)
		}
	}
}

func TestSecurityHeadersRemainIndependent(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	first, second := httptest.NewRecorder(), httptest.NewRecorder()
	handler.ServeHTTP(first, req)
	want := first.Header().Clone()
	for name, values := range first.Header() {
		values[0] = "changed"
		first.Header()[name] = append(values, "appended")
	}
	handler.ServeHTTP(second, req)
	if !reflect.DeepEqual(second.Header(), want) {
		t.Fatal("mutating one response changed another response")
	}
	for name, values := range second.Header() {
		values = append(values, "appended")
		values[0] = "changed"
		for other, otherValues := range second.Header() {
			if other != name && !reflect.DeepEqual(otherValues, want[other]) {
				t.Fatalf("mutating %s changed %s", name, other)
			}
		}
	}
}

func TestSecurityHeadersValuesAndHSTSConditions(t *testing.T) {
	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; base-uri 'self'; object-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; connect-src 'self' ws: wss:; frame-ancestors 'none'; form-action 'self'; upgrade-insecure-requests; block-all-mixed-content",
		"X-Content-Type-Options":  "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "strict-origin-when-cross-origin",
		"Permissions-Policy": "geolocation=(), microphone=(), camera=()", "Cross-Origin-Opener-Policy": "same-origin",
		"Cross-Origin-Embedder-Policy": "require-corp", "Cross-Origin-Resource-Policy": "same-origin", "Origin-Agent-Cluster": "?1",
	}
	for _, fixture := range []struct {
		scheme, forwarded string
		hsts              bool
	}{
		{"http", "", false}, {"http", "http", false}, {"http", "HTTPS", true}, {"https", "", true},
	} {
		req := httptest.NewRequest(http.MethodGet, fixture.scheme+"://example.com/", nil)
		req.Header.Set("X-Forwarded-Proto", fixture.forwarded)
		response := httptest.NewRecorder()
		response.Header().Set("X-Frame-Options", "obsolete")
		handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for name, value := range want {
				if w.Header().Get(name) != value {
					t.Errorf("%s differs before the next handler", name)
				}
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		handler.ServeHTTP(response, req)
		hsts := response.Header().Get("Strict-Transport-Security")
		if fixture.hsts && hsts != "max-age=31536000; includeSubDomains" || !fixture.hsts && hsts != "" {
			t.Errorf("unexpected HSTS %q for %+v", hsts, fixture)
		}
		if response.Code != http.StatusNoContent {
			t.Fatal("next handler was not called")
		}
	}
}
