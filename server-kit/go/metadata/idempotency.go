package metadata

import (
	"net/http"
	"strings"
)

// IsMutatingMethod reports whether an HTTP method changes server state. It is
// the authoritative read/mutate split for command-metadata enforcement: the
// RFC 9110 "safe" methods (GET, HEAD, OPTIONS, TRACE) are reads; everything
// else (POST, PUT, PATCH, DELETE, and any non-standard verb) is treated as a
// mutation. An empty or unknown method fails safe to mutating, so a caller
// that cannot classify a request still demands the mutation-grade metadata.
func IsMutatingMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

// RequiresIdempotency reports whether a command dispatched with the given HTTP
// method must carry a client idempotency key. Reads are naturally idempotent,
// so only mutations require one — this is the contract testing_practices.md
// TE-08 describes ("idempotency scoped to mutating commands"). Enforcement
// layers should gate the idempotency-key requirement on this predicate so a
// pure read (GET /v1/menu/menus, a wallet balance, a favourites list) is never
// rejected with idempotency_required.
func RequiresIdempotency(method string) bool {
	return IsMutatingMethod(method)
}
