package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSONAndWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]any{"ok": true})
	if rec.Code != http.StatusCreated || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected JSON response: status=%d content-type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode JSON response: %v", err)
	}
	if payload["ok"] != true {
		t.Fatalf("unexpected payload: %+v", payload)
	}

	rec = httptest.NewRecorder()
	WriteError(rec, http.StatusTeapot, "short and stout")
	if rec.Code != http.StatusTeapot {
		t.Fatalf("error status = %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("error content type = %q", rec.Header().Get("Content-Type"))
	}
}
