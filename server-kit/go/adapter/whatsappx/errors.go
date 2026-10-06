package whatsappx

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// OutcomeClass describes retry safety. Unknown mutations must be reconciled before retry.
type OutcomeClass string

const (
	OutcomePermanent OutcomeClass = "permanent"
	OutcomeTransient OutcomeClass = "transient"
	OutcomeUnknown   OutcomeClass = "unknown"
)

// ProviderFailure preserves machine-readable provider evidence without exposing message text.
type ProviderFailure struct {
	Code      int    `json:"code"`
	Subcode   int    `json:"error_subcode,omitempty"`
	Transient bool   `json:"is_transient,omitempty"`
	RequestID string `json:"fbtrace_id,omitempty"`
}

// ProviderError is safe for logs; raw provider messages and URLs are intentionally omitted.
type ProviderError struct {
	Operation  string
	HTTPStatus int
	Failure    ProviderFailure
	Class      OutcomeClass
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("whatsappx: %s failed (status=%d code=%d outcome=%s)", e.Operation, e.HTTPStatus, e.Failure.Code, e.Class)
}
func providerFailure(operation string, resp *http.Response, failure *ProviderFailure, ambiguous bool) *ProviderError {
	e := &ProviderError{Operation: operation, HTTPStatus: resp.StatusCode, Class: OutcomePermanent}
	if failure != nil {
		e.Failure = *failure
	}
	if e.Failure.RequestID == "" {
		e.Failure.RequestID = resp.Header.Get("X-FB-Request-ID")
	}
	if len(e.Failure.RequestID) > 256 || (e.Failure.RequestID != "" && !validPathID(e.Failure.RequestID)) {
		e.Failure.RequestID = ""
	}
	if resp.StatusCode == 429 || resp.StatusCode >= 500 || e.Failure.Transient {
		e.Class = OutcomeTransient
	}
	if ambiguous || ((operation == "send" || operation == "upload" || operation == "mark-read") && resp.StatusCode >= 500) {
		e.Class = OutcomeUnknown
	}
	if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 && seconds <= 86400 {
		e.RetryAfter = time.Duration(seconds) * time.Second
	}
	if e.RetryAfter == 0 {
		if date, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			delay := time.Until(date)
			if delay > 0 && delay <= 24*time.Hour {
				e.RetryAfter = delay
			}
		}
	}
	return e
}
