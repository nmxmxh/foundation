package domainerr

import (
	"errors"
	"net/http"
	"strings"
)

// Kind represents a stable domain error category.
type Kind string

const (
	KindValidation   Kind = "validation"
	KindConflict     Kind = "conflict"
	KindNotFound     Kind = "not_found"
	KindUnauthorized Kind = "unauthorized"
	KindForbidden    Kind = "forbidden"
	KindRateLimited  Kind = "rate_limited"
	KindUnavailable  Kind = "unavailable"
	KindInternal     Kind = "internal"
)

// Error is a typed domain error used across services and transport layers.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = "operation failed"
	}
	return msg
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *Error) Is(target error) bool {
	typed, ok := target.(*Error)
	if !ok || typed == nil {
		return false
	}
	if typed.Kind != "" && e.Kind != typed.Kind {
		return false
	}
	if strings.TrimSpace(typed.Code) != "" && strings.TrimSpace(e.Code) != strings.TrimSpace(typed.Code) {
		return false
	}
	return true
}

func New(kind Kind, code, message string, cause error) *Error {
	if kind == "" {
		kind = KindInternal
	}
	if strings.TrimSpace(code) == "" {
		code = "unknown_error"
	}
	if strings.TrimSpace(message) == "" {
		message = "operation failed"
	}
	return &Error{
		Kind:    kind,
		Code:    strings.TrimSpace(code),
		Message: strings.TrimSpace(message),
		Cause:   cause,
	}
}

func Validation(code, message string) *Error {
	return New(KindValidation, code, message, nil)
}

func Conflict(code, message string) *Error {
	return New(KindConflict, code, message, nil)
}

func NotFound(code, message string) *Error {
	return New(KindNotFound, code, message, nil)
}

func Unauthorized(code, message string) *Error {
	return New(KindUnauthorized, code, message, nil)
}

func Forbidden(code, message string) *Error {
	return New(KindForbidden, code, message, nil)
}

func RateLimited(code, message string) *Error {
	return New(KindRateLimited, code, message, nil)
}

func Unavailable(code, message string) *Error {
	return New(KindUnavailable, code, message, nil)
}

func Internal(code, message string) *Error {
	return New(KindInternal, code, message, nil)
}

func KindOf(err error) Kind {
	var typed *Error
	if errors.As(err, &typed) && typed != nil && typed.Kind != "" {
		return typed.Kind
	}
	return KindInternal
}

func CodeOf(err error) string {
	var typed *Error
	if errors.As(err, &typed) && typed != nil {
		if strings.TrimSpace(typed.Code) != "" {
			return strings.TrimSpace(typed.Code)
		}
	}
	return "unknown_error"
}

func MessageOf(err error, fallback string) string {
	if err != nil {
		msg := strings.TrimSpace(err.Error())
		if msg != "" {
			return msg
		}
	}
	if strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback)
	}
	return "operation failed"
}

func HTTPStatus(err error) int {
	switch KindOf(err) {
	case KindValidation:
		return http.StatusBadRequest
	case KindConflict:
		return http.StatusConflict
	case KindNotFound:
		return http.StatusNotFound
	case KindUnauthorized:
		return http.StatusUnauthorized
	case KindForbidden:
		return http.StatusForbidden
	case KindRateLimited:
		return http.StatusTooManyRequests
	case KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// WithCause returns a copy of e carrying cause for logging and errors.Is/As
// chains. The cause is diagnostic context only: Body/WriteHTTP never serialize
// it, so services can attach raw infrastructure errors without leaking them.
func (e *Error) WithCause(cause error) *Error {
	if e == nil {
		return nil
	}
	out := *e
	out.Cause = cause
	return &out
}

// CauseOf returns the diagnostic cause carried by a domain error, or nil.
func CauseOf(err error) error {
	var typed *Error
	if errors.As(err, &typed) && typed != nil {
		return typed.Cause
	}
	return nil
}
