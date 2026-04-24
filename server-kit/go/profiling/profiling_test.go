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
