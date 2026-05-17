package versioning

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractVersionStrategiesAndMetadata(t *testing.T) {
	v := New(Config{
		Strategy:          StrategyHeader,
		DefaultVersion:    "v2",
		SupportedVersions: []string{"v1", "v2"},
	})
	if versions := v.SupportedVersions(); len(versions) != 2 || versions[0] != "v1" || versions[1] != "v2" {
		t.Fatalf("supported versions = %+v", versions)
	}
	if all := v.AllVersions(); len(all) != 2 || all[1].Status != StatusCurrent {
		t.Fatalf("all versions = %+v", all)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Version", "v1")
	if got := v.ExtractVersion(req); got != "v1" {
		t.Fatalf("header version = %q", got)
	}
	if got := New(Config{Strategy: StrategyPath}).ExtractVersion(httptest.NewRequest(http.MethodGet, "/v2/users", nil)); got != "v2" {
		t.Fatalf("path version = %q", got)
	}
	if got := New(Config{Strategy: StrategyQuery}).ExtractVersion(httptest.NewRequest(http.MethodGet, "/?version=v3", nil)); got != "v3" {
		t.Fatalf("query version = %q", got)
	}
	accept := httptest.NewRequest(http.MethodGet, "/", nil)
	accept.Header.Set("Accept", "application/vnd.ovasabi.v4+json")
	if got := New(Config{Strategy: StrategyAccept, VendorName: "ovasabi"}).ExtractVersion(accept); got != "v4" {
		t.Fatalf("accept version = %q", got)
	}
	if extractAcceptVersion("application/json", "") != "" || extractPathVersion("/api/v1/users") != "" {
		t.Fatal("unexpected version extraction")
	}
}

func TestMiddlewareRouterAndVersionedHandler(t *testing.T) {
	mismatched := ""
	deprecated := ""
	v := New(Config{
		Strategy:          StrategyPath,
		DefaultVersion:    "v1",
		SupportedVersions: []string{"v1", "v2", "v3"},
		OnVersionMismatch: func(requested string, _ []string) { mismatched = requested },
		DeprecationHandler: func(version string, _ time.Time, _ *time.Time) {
			deprecated = version
		},
	})
	sunset := time.Now().Add(time.Hour)
	v.DeprecateVersion("v2", &sunset)
	v.RegisterVersion(Version{Name: "v3", Status: StatusSunset})
	v.HandleVersion("v1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users" {
			t.Fatalf("path not stripped: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
	}))

	next := v.Middleware(v.Router())
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/users", nil))
	if rec.Code != http.StatusAccepted || rec.Header().Get("X-API-Version") != "v1" {
		t.Fatalf("v1 response code=%d headers=%+v", rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	next.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2/users", nil))
	if rec.Code != http.StatusNotFound || rec.Header().Get("Deprecation") != "true" || deprecated != "v2" {
		t.Fatalf("deprecated response code=%d deprecated=%q headers=%+v", rec.Code, deprecated, rec.Header())
	}
	rec = httptest.NewRecorder()
	next.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v3/users", nil))
	if rec.Code != http.StatusGone {
		t.Fatalf("sunset response code=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	next.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v9/users", nil))
	if rec.Code != http.StatusBadRequest || mismatched != "v9" {
		t.Fatalf("mismatch response code=%d requested=%q", rec.Code, mismatched)
	}

	vh := NewVersionedHandler().
		Handle("v1", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) }).
		Fallback(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	rec = httptest.NewRecorder()
	vh.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ContextWithVersion(nilContext(), "v1")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("versioned handler code = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	vh.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("fallback handler code = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	NewVersionedHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing handler code = %d", rec.Code)
	}
}

func TestVersionsHandlerParseAndCompare(t *testing.T) {
	v := New(Config{SupportedVersions: []string{"v2", "v1"}})
	rec := httptest.NewRecorder()
	v.VersionsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/versions", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode versions body: %v", err)
	}
	if body["current_version"] != "v1" {
		t.Fatalf("unexpected versions body: %+v", body)
	}
	if major, minor, ok := ParseVersion("v2.1"); !ok || major != 2 || minor != 1 {
		t.Fatalf("ParseVersion = %d %d %v", major, minor, ok)
	}
	if _, _, ok := ParseVersion("vbad"); ok {
		t.Fatal("expected invalid version parse")
	}
	if CompareVersions("v1", "v2") != -1 || CompareVersions("v2.1", "v2") != 1 || CompareVersions("v2", "v2.0") != 0 {
		t.Fatal("CompareVersions failed")
	}
	if VersionFromContext(nilContext()) != "" {
		t.Fatal("empty context version expected")
	}
}

func nilContext() context.Context {
	return context.Background()
}
