package profiling

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerRequiresEnablementAndAuthorization(t *testing.T) {
	disabled := httptest.NewRecorder()
	Handler(Config{}).ServeHTTP(disabled, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if disabled.Code != http.StatusNotFound {
		t.Fatalf("disabled status = %d, want 404", disabled.Code)
	}

	forbidden := httptest.NewRecorder()
	Handler(Config{Enabled: true, Authorize: func(*http.Request) bool { return false }}).
		ServeHTTP(forbidden, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("forbidden status = %d, want 403", forbidden.Code)
	}
}

func TestHandlerServesDefaultAndCustomIndex(t *testing.T) {
	allowed := httptest.NewRecorder()
	Handler(Config{Enabled: true}).ServeHTTP(allowed, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if allowed.Code != http.StatusOK {
		t.Fatalf("default index status = %d, want 200", allowed.Code)
	}

	custom := httptest.NewRecorder()
	Handler(Config{
		Enabled:         true,
		AdminPathPrefix: "/admin/profile/",
		Authorize:       func(*http.Request) bool { return true },
	}).ServeHTTP(custom, httptest.NewRequest(http.MethodGet, "/admin/profile/", nil))
	if custom.Code != http.StatusOK {
		t.Fatalf("custom index status = %d, want 200", custom.Code)
	}
}
