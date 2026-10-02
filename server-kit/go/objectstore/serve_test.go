package objectstore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKeyWithinPrefixResistsTraversal(t *testing.T) {
	cases := []struct {
		key, prefix string
		want        bool
	}{
		{"verification/org/v/id/x.jpg", "verification/", true},
		{"verification", "verification", true},
		{"./verification/x", "verification", true},
		{"dish/../verification/x", "verification", true},
		{"/verification/x", "verification/", true},
		{"verificationx/y", "verification", false},
		{"dish/a/b.jpg", "verification", false},
		{"anything", "", true},
	}
	for _, c := range cases {
		if got := KeyWithinPrefix(c.key, c.prefix); got != c.want {
			t.Errorf("KeyWithinPrefix(%q, %q) = %v, want %v", c.key, c.prefix, got, c.want)
		}
	}
}

func TestServeObjectHeadersFollowVisibility(t *testing.T) {
	store := stubOpener{body: "bytes", obj: Object{ContentType: "image/jpeg", Size: 5}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if err := ServeObject(rec, req, store, "k", ServeOptions{Visibility: VisibilityPrivate}); err != nil {
		t.Fatalf("private serve: %v", err)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("private Cache-Control = %q, want no-store", got)
	}
	if rec.Header().Get("Cross-Origin-Resource-Policy") != "" {
		t.Fatal("private object must not be marked cross-origin embeddable")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Body.String() != "bytes" {
		t.Fatal("private serve missing nosniff or body")
	}

	rec = httptest.NewRecorder()
	if err := ServeObject(rec, req, store, "k", ServeOptions{Visibility: VisibilityPublic, CrossOriginEmbed: true}); err != nil {
		t.Fatalf("public serve: %v", err)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") ||
		rec.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Fatalf("public headers wrong: %v", rec.Header())
	}

	rec = httptest.NewRecorder()
	head := httptest.NewRequest(http.MethodHead, "/x", nil)
	if err := ServeObject(rec, head, store, "k", ServeOptions{}); err != nil || rec.Body.Len() != 0 {
		t.Fatalf("HEAD must write headers only: err=%v body=%d", err, rec.Body.Len())
	}

	post := httptest.NewRequest(http.MethodPost, "/x", nil)
	if err := ServeObject(httptest.NewRecorder(), post, store, "k", ServeOptions{}); !errors.Is(err, ErrServeMethod) {
		t.Fatalf("POST: got %v, want ErrServeMethod", err)
	}
	missing := stubOpener{err: errors.New("no such object")}
	rec = httptest.NewRecorder()
	if err := ServeObject(rec, req, missing, "k", ServeOptions{}); err == nil || rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatal("a failed open must return an error and write nothing")
	}
}

func TestServeObjectEnforcesSandboxOnExecutableTypes(t *testing.T) {
	dangerousTypes := []string{"image/svg+xml", "text/html", "text/xml", "application/xml"}
	for _, dt := range dangerousTypes {
		store := stubOpener{body: "<svg></svg>", obj: Object{ContentType: dt, Size: 11}}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/media/test", nil)

		// Even if public visibility is requested, dangerous types must be sandboxed
		if err := ServeObject(rec, req, store, "k", ServeOptions{Visibility: VisibilityPublic}); err != nil {
			t.Fatalf("serve error: %v", err)
		}

		if got := rec.Header().Get("Content-Disposition"); got != "attachment" {
			t.Errorf("type %s: expected attachment disposition, got %q", dt, got)
		}
		if got := rec.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") {
			t.Errorf("type %s: expected sandbox CSP, got %q", dt, got)
		}
	}
}

// TestSniffUploadRejectsScriptCapableTypes locks the accept/serve symmetry.
// SniffUpload has no signature for script-capable types, so a caller cannot store
// them through the upload path. ServeObject keeps its own guard for objects that
// arrive by migration or a direct bucket write.

type stubOpener struct {
	body string
	obj  Object
	err  error
}

func (s stubOpener) Open(context.Context, string) (io.ReadCloser, Object, error) {
	if s.err != nil {
		return nil, Object{}, s.err
	}
	return io.NopCloser(strings.NewReader(s.body)), s.obj, nil
}
