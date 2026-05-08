package domainerr

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDomainErrorDefaultsAndMatching(t *testing.T) {
	cause := errors.New("root cause")
	err := New("", " ", " ", cause)
	if err.Kind != KindInternal || err.Code != "unknown_error" || err.Error() != "operation failed" {
		t.Fatalf("unexpected defaults: %+v error=%q", err, err.Error())
	}
	if !errors.Is(err, &Error{Kind: KindInternal, Code: "unknown_error"}) {
		t.Fatal("expected errors.Is to match kind and code")
	}
	if !errors.Is(err, cause) {
		t.Fatal("expected cause to unwrap")
	}
	if (*Error)(nil).Error() != "" || (*Error)(nil).Unwrap() != nil {
		t.Fatal("nil error methods should be safe")
	}
}

func TestDomainErrorHelpersAndHTTPStatus(t *testing.T) {
	cases := []struct {
		err    error
		kind   Kind
		code   string
		status int
	}{
		{Validation("invalid", "bad input"), KindValidation, "invalid", http.StatusBadRequest},
		{Conflict("conflict", "already exists"), KindConflict, "conflict", http.StatusConflict},
		{NotFound("missing", "not found"), KindNotFound, "missing", http.StatusNotFound},
		{Unauthorized("auth", "missing token"), KindUnauthorized, "auth", http.StatusUnauthorized},
		{Forbidden("deny", "denied"), KindForbidden, "deny", http.StatusForbidden},
		{RateLimited("rate", "slow down"), KindRateLimited, "rate", http.StatusTooManyRequests},
		{Unavailable("down", "dependency down"), KindUnavailable, "down", http.StatusServiceUnavailable},
		{Internal("boom", "failed"), KindInternal, "boom", http.StatusInternalServerError},
	}
	for _, tc := range cases {
		if got := KindOf(tc.err); got != tc.kind {
			t.Fatalf("KindOf() = %s, want %s", got, tc.kind)
		}
		if got := CodeOf(tc.err); got != tc.code {
			t.Fatalf("CodeOf() = %s, want %s", got, tc.code)
		}
		if got := HTTPStatus(tc.err); got != tc.status {
			t.Fatalf("HTTPStatus() = %d, want %d", got, tc.status)
		}
	}
	if CodeOf(errors.New("plain")) != "unknown_error" {
		t.Fatal("plain errors should use unknown_error code")
	}
	if MessageOf(nil, " fallback ") != "fallback" {
		t.Fatal("expected fallback message to be trimmed")
	}
}

func TestBodyAndWriteHTTP(t *testing.T) {
	details := map[string]any{"field": "name"}
	body := Body(errors.New("method"), ResponseOptions{
		Status:        http.StatusMethodNotAllowed,
		EventType:     "user:create:failed",
		CorrelationID: "corr_1",
		Details:       details,
	})
	if body.State != "failed" || body.Error.Kind != string(KindValidation) || body.Error.Status != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected body: %+v", body)
	}
	details["field"] = "mutated"
	if body.Error.Details["field"] != "name" {
		t.Fatal("expected response details to be cloned")
	}

	rec := httptest.NewRecorder()
	status := WriteHTTP(rec, NotFound("missing", "not found"), ResponseOptions{})
	if status != http.StatusNotFound || rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d recorder = %d", status, rec.Code)
	}
	var response Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "missing" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if WriteHTTP(nil, errors.New("x"), ResponseOptions{}) != 0 {
		t.Fatal("nil writer should return zero")
	}
}
