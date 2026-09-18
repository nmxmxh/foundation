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

func TestSniffUploadAcceptsRealSignatures(t *testing.T) {
	cases := map[string]string{
		"image/jpeg":      "\xFF\xD8\xFF\xE0rest",
		"image/JPG":       "\xFF\xD8\xFF\xE0rest",
		"image/png":       "\x89PNG\r\n\x1a\nrest",
		"image/gif":       "GIF89arest",
		"image/webp":      "RIFF\x00\x00\x00\x00WEBPVP8 ",
		"application/pdf": "%PDF-1.7\n",
		"video/mp4":       "\x00\x00\x00\x18ftypmp42rest",
		"video/quicktime": "\x00\x00\x00\x14ftypqt  rest",
		"video/webm":      "\x1A\x45\xDF\xA3rest",
	}
	for declared, body := range cases {
		reader, err := SniffUpload(strings.NewReader(body), declared)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", declared, err)
		}
		replayed, _ := io.ReadAll(reader)
		if string(replayed) != body {
			t.Fatalf("%s: body not replayed intact", declared)
		}
	}
}

func TestSniffUploadRefusesMismatchAndUnknown(t *testing.T) {
	if _, err := SniffUpload(strings.NewReader("<html><script>alert(1)</script>"), "image/png"); !errors.Is(err, ErrContentMismatch) {
		t.Fatalf("html declared as png: got %v, want ErrContentMismatch", err)
	}
	if _, err := SniffUpload(strings.NewReader(""), "image/jpeg"); !errors.Is(err, ErrContentMismatch) {
		t.Fatalf("empty body: got %v, want ErrContentMismatch", err)
	}
	if _, err := SniffUpload(strings.NewReader("anything"), "text/html"); !errors.Is(err, ErrContentTypeUnsupported) {
		t.Fatalf("unsupported type: got %v, want ErrContentTypeUnsupported", err)
	}
	if !SniffSupported("video/mp4") || SniffSupported("text/plain") {
		t.Fatal("SniffSupported disagrees with the signature table")
	}
}

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
