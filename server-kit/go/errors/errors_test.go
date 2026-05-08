package errors

import (
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodeHTTPStatusAndClassification(t *testing.T) {
	cases := map[Code]int{
		CodeBadRequest:     http.StatusBadRequest,
		CodeUnauthorized:   http.StatusUnauthorized,
		CodeForbidden:      http.StatusForbidden,
		CodeNotFound:       http.StatusNotFound,
		CodeConflict:       http.StatusConflict,
		CodeDuplicate:      http.StatusConflict,
		CodeGone:           http.StatusGone,
		CodeExpired:        http.StatusGone,
		CodeValidation:     http.StatusUnprocessableEntity,
		CodeRateLimited:    http.StatusTooManyRequests,
		CodeQuotaExceeded:  http.StatusTooManyRequests,
		CodePrecondition:   http.StatusPreconditionFailed,
		CodeInvalidState:   http.StatusPreconditionFailed,
		CodeNotImplemented: http.StatusNotImplemented,
		CodeUnavailable:    http.StatusServiceUnavailable,
		CodeDependency:     http.StatusServiceUnavailable,
		CodeTimeout:        http.StatusGatewayTimeout,
		CodeInternal:       http.StatusInternalServerError,
	}
	for code, status := range cases {
		if got := code.HTTPStatus(); got != status {
			t.Fatalf("%s status = %d, want %d", code, got, status)
		}
		if code.IsClientError() != (status >= 400 && status < 500) {
			t.Fatalf("%s client classification mismatch", code)
		}
		if code.IsServerError() != (status >= 500) {
			t.Fatalf("%s server classification mismatch", code)
		}
	}
}

func TestErrorWrappingDetailsAndAPIResponse(t *testing.T) {
	base := New(CodeNotFound, "missing").WithField("id", "123").WithRequestID("req_1")
	wrapped := Wrap(base, CodeDependency, "lookup failed").WithFields(map[string]interface{}{"dep": "db"})
	if wrapped == nil || !stderrors.Is(wrapped, base) {
		t.Fatal("expected wrapped error to preserve cause")
	}
	if wrapped.Details["id"] != "123" || wrapped.Details["dep"] != "db" {
		t.Fatalf("details not preserved: %+v", wrapped.Details)
	}
	if !strings.Contains(wrapped.Error(), "DEPENDENCY_ERROR: lookup failed") {
		t.Fatalf("unexpected error string: %s", wrapped.Error())
	}
	if Wrap(nil, CodeInternal, "nil") != nil {
		t.Fatal("wrapping nil should return nil")
	}
	if !Is(base, CodeNotFound) || Is(base, CodeConflict) || Is(nil, CodeInternal) {
		t.Fatal("Is code matching failed")
	}
	if got, ok := As(wrapped); !ok || got.Code != CodeDependency {
		t.Fatalf("As() = %+v %v", got, ok)
	}
	if GetCode(stderrors.New("plain")) != CodeInternal || GetCode(nil) != "" {
		t.Fatal("GetCode fallback failed")
	}
	api := base.ToAPIResponse()
	if api.Error.Code != string(CodeNotFound) || api.Error.RequestID != "req_1" {
		t.Fatalf("unexpected api response: %+v", api)
	}
	if len(base.ToJSON()) == 0 {
		t.Fatal("expected json encoding")
	}
}

func TestHTTPErrorAndRetryClassification(t *testing.T) {
	rec := httptest.NewRecorder()
	HTTPError(rec, BadRequest("bad"))
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected http response: code=%d content-type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = httptest.NewRecorder()
	HTTPError(rec, stderrors.New("plain"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("plain error status = %d", rec.Code)
	}
	if !IsTransient(Unavailable("down")) || !IsTransient(Timeout("slow")) || !IsTransient(Dependency("dep")) || !IsTransient(RateLimited("rate")) {
		t.Fatal("expected transient errors")
	}
	if IsTransient(NotFound("missing")) || IsTransient(nil) {
		t.Fatal("unexpected transient classification")
	}
	if !IsPermanent(Validation("bad")) || !ShouldRetry(Dependency("plain")) {
		t.Fatal("retry/permanent classification failed")
	}
	constructors := []*Error{
		Unauthorized("x"), Forbidden("x"), Conflict("x"), Internal("x"), Duplicate("x"),
		InvalidState("x"), Expired("x"), QuotaExceeded("x"), PaymentFailed("x"),
	}
	for _, err := range constructors {
		if err.Message != "x" {
			t.Fatalf("constructor failed: %+v", err)
		}
	}
	if Newf(CodeBadRequest, "bad %s", "input").Message != "bad input" {
		t.Fatal("Newf formatting failed")
	}
}

func TestAdditionalErrorHelpers(t *testing.T) {
	base := stderrors.New("root")
	wrapped := Wrapf(base, CodeExternalAPI, "partner %s", "failed")
	if wrapped == nil || wrapped.Code != CodeExternalAPI || !strings.Contains(wrapped.Message, "partner failed") {
		t.Fatalf("Wrapf() = %+v", wrapped)
	}
	if wrapped.WithCause(base) != wrapped || !stderrors.Is(wrapped, base) {
		t.Fatalf("WithCause did not preserve fluent error")
	}
	if !wrapped.IsCode(CodeExternalAPI) || wrapped.IsCode(CodeInternal) {
		t.Fatalf("IsCode failed")
	}
	if ExternalAPI("api").Code != CodeExternalAPI {
		t.Fatalf("ExternalAPI constructor failed")
	}
	if Is(stderrors.New("plain"), CodeInternal) {
		t.Fatalf("plain error should not match code")
	}
	if got, ok := As(stderrors.New("plain")); ok || got != nil {
		t.Fatalf("plain As = %+v %v", got, ok)
	}
}
