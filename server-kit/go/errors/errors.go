// Package errors provides a formalized error taxonomy with categorized error codes.
// It extends the standard error handling with domain-specific error types.
//
// Usage:
//
//	err := errors.New(errors.CodeNotFound, "user not found").
//	    WithField("user_id", userID).
//	    WithCause(dbErr)
//
//	if errors.Is(err, errors.CodeNotFound) {
//	    // handle not found
//	}
package errors

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"runtime"
	"strings"
)

// Code represents an error code category.
type Code string

// Error code categories
const (
	// Client errors (4xx)
	CodeBadRequest   Code = "BAD_REQUEST"      // 400: Malformed request
	CodeUnauthorized Code = "UNAUTHORIZED"     // 401: Missing or invalid auth
	CodeForbidden    Code = "FORBIDDEN"        // 403: Insufficient permissions
	CodeNotFound     Code = "NOT_FOUND"        // 404: Resource not found
	CodeConflict     Code = "CONFLICT"         // 409: Resource conflict
	CodeGone         Code = "GONE"             // 410: Resource no longer available
	CodeValidation   Code = "VALIDATION_ERROR" // 422: Validation failed
	CodeRateLimited  Code = "RATE_LIMITED"     // 429: Too many requests
	CodePrecondition Code = "PRECONDITION"     // 412: Precondition failed

	// Server errors (5xx)
	CodeInternal       Code = "INTERNAL_ERROR"      // 500: Unexpected error
	CodeNotImplemented Code = "NOT_IMPLEMENTED"     // 501: Feature not available
	CodeUnavailable    Code = "SERVICE_UNAVAILABLE" // 503: Temporary unavailable
	CodeTimeout        Code = "TIMEOUT"             // 504: Operation timed out
	CodeDependency     Code = "DEPENDENCY_ERROR"    // 503: Downstream service failed

	// Domain-specific codes
	CodeDuplicate     Code = "DUPLICATE"      // Already exists
	CodeInvalidState  Code = "INVALID_STATE"  // Invalid state transition
	CodeExpired       Code = "EXPIRED"        // Token/resource expired
	CodeQuotaExceeded Code = "QUOTA_EXCEEDED" // Limit reached
	CodePaymentFailed Code = "PAYMENT_FAILED" // Payment processing error
	CodeExternalAPI   Code = "EXTERNAL_API"   // Third-party API error
)

// HTTPStatus returns the HTTP status code for an error code.
func (c Code) HTTPStatus() int {
	switch c {
	case CodeBadRequest:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict, CodeDuplicate:
		return http.StatusConflict
	case CodeGone, CodeExpired:
		return http.StatusGone
	case CodeValidation:
		return http.StatusUnprocessableEntity
	case CodeRateLimited, CodeQuotaExceeded:
		return http.StatusTooManyRequests
	case CodePrecondition, CodeInvalidState:
		return http.StatusPreconditionFailed
	case CodeNotImplemented:
		return http.StatusNotImplemented
	case CodeUnavailable, CodeDependency:
		return http.StatusServiceUnavailable
	case CodeTimeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

// IsClientError returns true if this is a client error (4xx).
func (c Code) IsClientError() bool {
	status := c.HTTPStatus()
	return status >= 400 && status < 500
}

// IsServerError returns true if this is a server error (5xx).
func (c Code) IsServerError() bool {
	status := c.HTTPStatus()
	return status >= 500
}

// Error represents a structured application error.
type Error struct {
	// Code is the error code category.
	Code Code `json:"code"`

	// Message is the human-readable error message.
	Message string `json:"message"`

	// Details provides additional context.
	Details map[string]interface{} `json:"details,omitempty"`

	// Cause is the underlying error (not serialized).
	Cause error `json:"-"`

	// Stack is the stack trace (not serialized in production).
	Stack string `json:"-"`

	// RequestID links to the request that caused this error.
	RequestID string `json:"request_id,omitempty"`

	// Timestamp is when the error occurred.
	Timestamp string `json:"timestamp,omitempty"`
}

// New creates a new Error with the given code and message.
func New(code Code, message string) *Error {
	return &Error{
		Code:    code,
		Message: message,
		Details: make(map[string]interface{}),
		Stack:   captureStack(2),
	}
}

// Newf creates a new Error with a formatted message.
func Newf(code Code, format string, args ...interface{}) *Error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
		Details: make(map[string]interface{}),
		Stack:   captureStack(2),
	}
}

// Wrap wraps an existing error with additional context.
func Wrap(err error, code Code, message string) *Error {
	if err == nil {
		return nil
	}

	e := &Error{
		Code:    code,
		Message: message,
		Details: make(map[string]interface{}),
		Cause:   err,
		Stack:   captureStack(2),
	}

	// Inherit details from wrapped Error
	if appErr, ok := err.(*Error); ok {
		for k, v := range appErr.Details {
			e.Details[k] = v
		}
	}

	return e
}

// Wrapf wraps an error with a formatted message.
func Wrapf(err error, code Code, format string, args ...interface{}) *Error {
	return Wrap(err, code, fmt.Sprintf(format, args...))
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying error for errors.Is/As support.
func (e *Error) Unwrap() error {
	return e.Cause
}

// WithField adds a detail field to the error.
func (e *Error) WithField(key string, value interface{}) *Error {
	e.Details[key] = value
	return e
}

// WithFields adds multiple detail fields to the error.
func (e *Error) WithFields(fields map[string]interface{}) *Error {
	for k, v := range fields {
		e.Details[k] = v
	}
	return e
}

// WithCause sets the underlying cause.
func (e *Error) WithCause(cause error) *Error {
	e.Cause = cause
	return e
}

// WithRequestID sets the request ID.
func (e *Error) WithRequestID(id string) *Error {
	e.RequestID = id
	return e
}

// HTTPStatus returns the appropriate HTTP status code.
func (e *Error) HTTPStatus() int {
	return e.Code.HTTPStatus()
}

// IsCode checks if the error has a specific code.
func (e *Error) IsCode(code Code) bool {
	return e.Code == code
}

// ToJSON returns the error as JSON bytes.
func (e *Error) ToJSON() []byte {
	data, err := json.Marshal(e)
	if err != nil {
		return []byte(`{"error":{"code":"INTERNAL","message":"failed to encode error"}}`)
	}
	return data
}

// APIResponse returns an API-safe representation.
type APIResponse struct {
	Error *APIError `json:"error"`
}

type APIError struct {
	Code      string                 `json:"code"`
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details,omitempty"`
	RequestID string                 `json:"request_id,omitempty"`
}

// ToAPIResponse converts to an API-safe response.
func (e *Error) ToAPIResponse() APIResponse {
	return APIResponse{
		Error: &APIError{
			Code:      string(e.Code),
			Message:   e.Message,
			Details:   e.Details,
			RequestID: e.RequestID,
		},
	}
}

// Is checks if an error matches a code (for use with errors.Is).
func Is(err error, code Code) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*Error); ok {
		return e.Code == code
	}
	return false
}

// As extracts an Error from an error chain.
func As(err error) (*Error, bool) {
	if err == nil {
		return nil, false
	}
	var e *Error
	if ok := stderrors.As(err, &e); ok {
		return e, true
	}
	return nil, false
}

// Code extracts the error code from an error.
func GetCode(err error) Code {
	if err == nil {
		return ""
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return CodeInternal
}

// HTTPError writes an error response to an HTTP response writer.
func HTTPError(w http.ResponseWriter, err error) {
	var e *Error
	if appErr, ok := err.(*Error); ok {
		e = appErr
	} else {
		e = New(CodeInternal, err.Error())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.HTTPStatus())
	json.NewEncoder(w).Encode(e.ToAPIResponse())
}

// captureStack captures the current stack trace.
func captureStack(skip int) string {
	const depth = 32
	var pcs [depth]uintptr
	n := runtime.Callers(skip+1, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])

	var builder strings.Builder
	for {
		frame, more := frames.Next()
		builder.WriteString(fmt.Sprintf("%s\n\t%s:%d\n", frame.Function, frame.File, frame.Line))
		if !more {
			break
		}
	}
	return builder.String()
}

// Convenience constructors

// BadRequest creates a bad request error.
func BadRequest(message string) *Error {
	return New(CodeBadRequest, message)
}

// Unauthorized creates an unauthorized error.
func Unauthorized(message string) *Error {
	return New(CodeUnauthorized, message)
}

// Forbidden creates a forbidden error.
func Forbidden(message string) *Error {
	return New(CodeForbidden, message)
}

// NotFound creates a not found error.
func NotFound(message string) *Error {
	return New(CodeNotFound, message)
}

// Conflict creates a conflict error.
func Conflict(message string) *Error {
	return New(CodeConflict, message)
}

// Validation creates a validation error.
func Validation(message string) *Error {
	return New(CodeValidation, message)
}

// Internal creates an internal error.
func Internal(message string) *Error {
	return New(CodeInternal, message)
}

// Unavailable creates a service unavailable error.
func Unavailable(message string) *Error {
	return New(CodeUnavailable, message)
}

// Timeout creates a timeout error.
func Timeout(message string) *Error {
	return New(CodeTimeout, message)
}

// RateLimited creates a rate limited error.
func RateLimited(message string) *Error {
	return New(CodeRateLimited, message)
}

// Duplicate creates a duplicate error.
func Duplicate(message string) *Error {
	return New(CodeDuplicate, message)
}

// InvalidState creates an invalid state error.
func InvalidState(message string) *Error {
	return New(CodeInvalidState, message)
}

// Expired creates an expired error.
func Expired(message string) *Error {
	return New(CodeExpired, message)
}

// Dependency creates a dependency error.
func Dependency(message string) *Error {
	return New(CodeDependency, message)
}

// ExternalAPI creates an external API error.
func ExternalAPI(message string) *Error {
	return New(CodeExternalAPI, message)
}

// QuotaExceeded creates a quota exceeded error.
func QuotaExceeded(message string) *Error {
	return New(CodeQuotaExceeded, message)
}

// PaymentFailed creates a payment failed error.
func PaymentFailed(message string) *Error {
	return New(CodePaymentFailed, message)
}

// IsTransient returns true if the error is transient and should be retried.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	e, ok := err.(*Error)
	if !ok {
		// Unknown errors are assumed transient
		return true
	}
	switch e.Code {
	case CodeUnavailable, CodeTimeout, CodeDependency, CodeRateLimited:
		return true
	default:
		return false
	}
}

// IsPermanent returns true if the error is permanent and should not be retried.
func IsPermanent(err error) bool {
	return !IsTransient(err)
}

// ShouldRetry returns true if the error should trigger a retry.
func ShouldRetry(err error) bool {
	return IsTransient(err)
}
