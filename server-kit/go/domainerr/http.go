package domainerr

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
)

// logSanitized is the diagnosability backstop for every rendered domain error:
// responses stay sanitized, but a server-side fault (5xx) or an attached cause
// must land in the structured log or outages become invisible to operators.
// Client-kind errors without a cause (plain 4xx denials) are not logged here.
func logSanitized(err error, status int, kind Kind, opts ResponseOptions) {
	cause := CauseOf(err)
	if cause == nil && status < http.StatusInternalServerError {
		return
	}
	detail := ""
	if cause != nil {
		detail = cause.Error()
	} else if err != nil {
		detail = err.Error()
	}
	// Server faults are errors; sanitized client denials that still carry a
	// cause are warnings so the error lane stays actionable.
	log := logger.Default().Error
	if status < http.StatusInternalServerError {
		log = logger.Default().Warn
	}
	log("request failed",
		"kind", string(kind),
		"code", CodeOf(err),
		"status", status,
		"event_type", strings.TrimSpace(opts.EventType),
		"correlation_id", strings.TrimSpace(opts.CorrelationID),
		"cause", detail,
	)
}

type Response struct {
	State string   `json:"state"`
	Error APIError `json:"error"`
}

type APIError struct {
	Kind          string           `json:"kind"`
	Code          string           `json:"code"`
	Message       string           `json:"message"`
	Status        int              `json:"status"`
	EventType     string           `json:"event_type,omitempty"`
	CorrelationID string           `json:"correlation_id,omitempty"`
	Details       extension.Object `json:"details,omitempty"`
}

type ResponseOptions struct {
	Status        int
	EventType     string
	CorrelationID string
	Details       extension.Object
}

func Body(err error, opts ResponseOptions) Response {
	status := opts.Status
	if status <= 0 {
		status = HTTPStatus(err)
	}
	kind := KindOf(err)
	if status == http.StatusMethodNotAllowed && kind == KindInternal {
		kind = KindValidation
	}
	logSanitized(err, status, kind, opts)
	return Response{
		State: "failed",
		Error: APIError{
			Kind:          string(kind),
			Code:          CodeOf(err),
			Message:       MessageOf(err, "operation failed"),
			Status:        status,
			EventType:     strings.TrimSpace(opts.EventType),
			CorrelationID: strings.TrimSpace(opts.CorrelationID),
			Details:       opts.Details.Clone(),
		},
	}
}

func WriteHTTP(w http.ResponseWriter, err error, opts ResponseOptions) int {
	if w == nil {
		return 0
	}
	body := Body(err, opts)
	status := body.Error.Status

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
	return status
}
