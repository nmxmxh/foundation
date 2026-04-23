package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
)

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func WriteError(w http.ResponseWriter, status int, message string) {
	domainerr.WriteHTTP(w, domainerr.New(domainerr.KindValidation, "request_error", message, nil), domainerr.ResponseOptions{
		Status: status,
	})
}
