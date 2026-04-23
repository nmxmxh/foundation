package domainerr

import (
	"encoding/json"
	"net/http"
	"strings"
)

type Response struct {
	State string   `json:"state"`
	Error APIError `json:"error"`
}

type APIError struct {
	Kind          string         `json:"kind"`
	Code          string         `json:"code"`
	Message       string         `json:"message"`
	Status        int            `json:"status"`
	EventType     string         `json:"event_type,omitempty"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

type ResponseOptions struct {
	Status        int
	EventType     string
	CorrelationID string
	Details       map[string]any
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
	return Response{
		State: "failed",
		Error: APIError{
			Kind:          string(kind),
			Code:          CodeOf(err),
			Message:       MessageOf(err, "operation failed"),
			Status:        status,
			EventType:     strings.TrimSpace(opts.EventType),
			CorrelationID: strings.TrimSpace(opts.CorrelationID),
			Details:       cloneDetails(opts.Details),
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

func cloneDetails(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
