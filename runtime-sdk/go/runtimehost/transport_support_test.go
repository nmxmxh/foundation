package runtimehost

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestProcessWorkerSnapshotJSONOmitsZeroTimes(t *testing.T) {
	encoded, err := json.Marshal(ProcessWorkerSnapshot{})
	if err != nil {
		t.Fatalf("Marshal(ProcessWorkerSnapshot{}) error = %v", err)
	}
	for _, field := range []string{`"last_started"`, `"last_success"`, `"last_failure"`} {
		if strings.Contains(string(encoded), field) {
			t.Fatalf("zero time field %s should be omitted: %s", field, encoded)
		}
	}

	withStarted := ProcessWorkerSnapshot{LastStarted: time.Unix(1, 0).UTC()}
	encoded, err = json.Marshal(withStarted)
	if err != nil {
		t.Fatalf("Marshal(withStarted) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"last_started"`) {
		t.Fatalf("non-zero last_started should be encoded: %s", encoded)
	}
}
