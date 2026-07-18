package domainerr

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
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
	if errors.Is(err, errors.New("different")) {
		t.Fatal("domain error unexpectedly matched a plain error")
	}
	if errors.Is(err, &Error{Kind: KindValidation}) {
		t.Fatal("domain error unexpectedly matched a different kind")
	}
	if errors.Is(err, &Error{Code: "different"}) {
		t.Fatal("domain error unexpectedly matched a different code")
	}
	if !errors.Is(err, &Error{}) {
		t.Fatal("empty domain target should match")
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
	if MessageOf(errors.New(" explicit "), "fallback") != "explicit" {
		t.Fatal("expected explicit message")
	}
	if MessageOf(errors.New(" "), " ") != "operation failed" {
		t.Fatal("expected default message")
	}
	if CodeOf(&Error{}) != "unknown_error" || KindOf(&Error{}) != KindInternal {
		t.Fatal("empty domain error should use stable defaults")
	}
}

func TestBodyAndWriteHTTP(t *testing.T) {
	details := extension.Object{"field": extension.String("name")}
	body := Body(errors.New("method"), ResponseOptions{
		Status:        http.StatusMethodNotAllowed,
		EventType:     "user:create:failed",
		CorrelationID: "corr_1",
		Details:       details,
	})
	if body.State != "failed" || body.Error.Kind != string(KindValidation) || body.Error.Status != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected body: %+v", body)
	}
	details["field"] = extension.String("mutated")
	if field, ok := body.Error.Details.GetString("field"); !ok || field != "name" {
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

func TestWithCauseAndCauseOf(t *testing.T) {
	cause := errors.New("underlying failure")
	err := Forbidden("deny", "denied").WithCause(cause)
	if !errors.Is(CauseOf(err), cause) {
		t.Fatalf("CauseOf() = %v, want %v", CauseOf(err), cause)
	}
	if !errors.Is(err, cause) {
		t.Fatal("expected cause to participate in errors.Is chains")
	}
	if CauseOf(errors.New("plain")) != nil {
		t.Fatal("plain errors carry no cause")
	}
	var nilErr *Error
	if nilErr.WithCause(cause) != nil {
		t.Fatal("nil receiver should stay nil")
	}
}

// captureLogs installs a buffer-backed JSON logger and restores the previous
// default when the test ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := logger.Default()
	l, err := logger.New(logger.Config{Format: "json", LogLevel: "debug", Output: &buf})
	if err != nil {
		t.Fatalf("logger.New() error = %v", err)
	}
	logger.SetDefault(l)
	t.Cleanup(func() { logger.SetDefault(prev) })
	return &buf
}

// Regression guard for the sanitized-error diagnosability contract: rendering
// a domain error must log the attached cause (which is never serialized to the
// client), and any 5xx must log even without one. Without this backstop a
// swallowed infrastructure error (e.g. a missing table) is invisible in logs.
func TestBodyLogsSanitizedFailures(t *testing.T) {
	buf := captureLogs(t)
	cause := errors.New(`relation "chow_user_credentials" does not exist`)
	body := Body(Internal("register_failed", "could not create account").WithCause(cause), ResponseOptions{EventType: "user:register"})
	if !strings.Contains(buf.String(), "chow_user_credentials") {
		t.Fatalf("expected cause in log output, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "register_failed") {
		t.Fatalf("expected code in log output, got: %s", buf.String())
	}
	if strings.Contains(body.Error.Message, "chow_user_credentials") {
		t.Fatalf("cause leaked into response message: %q", body.Error.Message)
	}

	buf.Reset()
	Body(Internal("boom_failed", "operation failed"), ResponseOptions{})
	if !strings.Contains(buf.String(), "boom_failed") {
		t.Fatalf("expected 5xx to log without a cause, got: %s", buf.String())
	}

	buf.Reset()
	Body(Forbidden("cart_profile_forbidden", "denied").WithCause(cause), ResponseOptions{CorrelationID: "corr_1"})
	if !strings.Contains(buf.String(), "cart_profile_forbidden") {
		t.Fatalf("expected 4xx with cause to log, got: %s", buf.String())
	}

	buf.Reset()
	Body(Forbidden("plain_denial", "denied"), ResponseOptions{})
	if buf.Len() != 0 {
		t.Fatalf("plain 4xx without cause should not log, got: %s", buf.String())
	}

	buf.Reset()
	Body(nil, ResponseOptions{Status: http.StatusInternalServerError})
	if !strings.Contains(buf.String(), "unknown_error") {
		t.Fatalf("nil error with forced 5xx should still log, got: %s", buf.String())
	}
}
