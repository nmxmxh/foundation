package objectstore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// Visibility decides how a served object may be cached and embedded.
type Visibility int

const (
	// VisibilityPublic objects are immutable and safe for shared caches.
	VisibilityPublic Visibility = iota
	// VisibilityPrivate objects are personal data. No cache may keep them.
	VisibilityPrivate
)

// Opener is the read side of FSStore and Store.
type Opener interface {
	Open(ctx context.Context, key string) (io.ReadCloser, Object, error)
}

// ServeOptions tunes ServeObject.
type ServeOptions struct {
	Visibility Visibility
	// CrossOriginEmbed allows another origin (for example, a native shell) to
	// embed a PUBLIC object in an <img>. Ignored for private objects.
	CrossOriginEmbed bool
}

// ErrServeMethod means the request was not GET or HEAD.
var ErrServeMethod = errors.New("objectstore: only GET and HEAD may read an object")

// ServeObject streams one object with headers that match its visibility.
//
// Callers own authorization: check the key and the actor BEFORE calling. An
// error means nothing was written, so the caller chooses the status (a
// missing object and a forbidden one should look the same to the client).
func ServeObject(w http.ResponseWriter, r *http.Request, store Opener, key string, opts ServeOptions) error {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return ErrServeMethod
	}
	if store == nil {
		return errors.New("objectstore: store is required")
	}
	rc, obj, err := store.Open(r.Context(), key)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	h := w.Header()
	if obj.ContentType != "" {
		h.Set("Content-Type", obj.ContentType)
	}
	if obj.Size > 0 {
		h.Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	}
	// The stored type was checked at upload; never let a browser guess another.
	h.Set("X-Content-Type-Options", "nosniff")
	switch opts.Visibility {
	case VisibilityPrivate:
		h.Set("Cache-Control", "private, no-store, max-age=0")
		h.Set("Content-Disposition", "inline")
		h.Set("Referrer-Policy", "no-referrer")
		// A private object must never run as a document in our origin.
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	default:
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		if opts.CrossOriginEmbed {
			h.Set("Cross-Origin-Resource-Policy", "cross-origin")
		}
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return nil
	}
	_, err = io.Copy(w, rc)
	return err
}

// KeyWithinPrefix reports whether key, once cleaned, sits under prefix.
//
// Use it for visibility decisions on raw request keys. A plain
// strings.HasPrefix is fooled by "public/../private/x" and by "./private/x",
// which a store then resolves to the private object.
func KeyWithinPrefix(key, prefix string) bool {
	cleanKey := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(key)), "/")
	cleanPrefix := strings.Trim(path.Clean("/"+strings.TrimSpace(prefix)), "/")
	if cleanPrefix == "" {
		return true
	}
	return cleanKey == cleanPrefix || strings.HasPrefix(cleanKey, cleanPrefix+"/")
}
