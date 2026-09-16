package objectstore

import (
	"context"
	"strings"
	"testing"
)

// Regression: Open had no content type to report, so every file came back as
// application/octet-stream. Served under nosniff, a cross-origin <img> of an
// octet-stream response is refused, which blanked every media image in the
// native shells.
func TestFSStoreOpenInfersContentTypeFromExtension(t *testing.T) {
	s := newFSStore(t)
	ctx := context.Background()
	cases := map[string]string{
		"seed/chef/amara.png":   "image/png",
		"dish/d-1/photo.jpg":    "image/jpeg",
		"dish/d-1/photo.webp":   "image/webp",
		"misc/blob-without-ext": "application/octet-stream",
	}
	for key, want := range cases {
		if _, err := s.PutStream(ctx, key, strings.NewReader("x"), 1, PutOptions{}); err != nil {
			t.Fatalf("PutStream(%q) error = %v", key, err)
		}
		rc, obj, err := s.Open(ctx, key)
		if err != nil {
			t.Fatalf("Open(%q) error = %v", key, err)
		}
		_ = rc.Close()
		if obj.ContentType != want {
			t.Errorf("Open(%q).ContentType = %q, want %q", key, obj.ContentType, want)
		}
	}
}

// An explicit content type from the caller always wins over the extension.
func TestFSStorePutKeepsExplicitContentType(t *testing.T) {
	s := newFSStore(t)
	obj, err := s.PutStream(context.Background(), "a/file.png", strings.NewReader("x"), 1, PutOptions{ContentType: "image/avif"})
	if err != nil {
		t.Fatalf("PutStream() error = %v", err)
	}
	if obj.ContentType != "image/avif" {
		t.Fatalf("ContentType = %q, want image/avif", obj.ContentType)
	}
}
