package metadata

import (
	"net/http"
	"testing"
)

func TestRequiresIdempotencyOnlyForMutations(t *testing.T) {
	reads := []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, "get", " Get "}
	for _, method := range reads {
		if RequiresIdempotency(method) {
			t.Errorf("RequiresIdempotency(%q) = true, want false for a read", method)
		}
		if IsMutatingMethod(method) {
			t.Errorf("IsMutatingMethod(%q) = true, want false for a read", method)
		}
	}

	mutations := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, "post"}
	for _, method := range mutations {
		if !RequiresIdempotency(method) {
			t.Errorf("RequiresIdempotency(%q) = false, want true for a mutation", method)
		}
	}
}

func TestRequiresIdempotencyFailsSafeForUnknownMethod(t *testing.T) {
	// An empty or unrecognized method cannot be proven a read, so it is treated
	// as a mutation and still demands an idempotency key.
	for _, method := range []string{"", "  ", "FROBNICATE"} {
		if !RequiresIdempotency(method) {
			t.Errorf("RequiresIdempotency(%q) = false, want true (fail safe)", method)
		}
	}
}
