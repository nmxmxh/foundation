package apidocs

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerServesSpecAndDocs(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "openapi.json")
	spec := `{"openapi":"3.0.0","info":{"title":"Example API","description":"Example docs"},"paths":{}}`
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	handler := New(Options{SpecPaths: []string{specPath}})
	if !handler.Loaded() {
		t.Fatalf("expected spec to load: %v", handler.LoadError())
	}

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeSpec(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("content type = %q", got)
	}
	if !strings.Contains(rec.Body.String(), `"openapi":"3.0.0"`) {
		t.Fatalf("spec body missing OpenAPI payload: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec = httptest.NewRecorder()
	handler.ServeDocs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("docs status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Example API") || !strings.Contains(body, "/openapi.json") {
		t.Fatalf("docs body missing title/spec url: %s", body)
	}
}

func TestHandlerMethodValidationAndMissingSpec(t *testing.T) {
	handler := New(Options{SpecPaths: []string{filepath.Join(t.TempDir(), "missing.json")}})

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeSpec(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing spec status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	req = httptest.NewRequest(http.MethodPost, "/docs", nil)
	rec = httptest.NewRecorder()
	handler.ServeDocs(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("allow = %q", got)
	}
}
