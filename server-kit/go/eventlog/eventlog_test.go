package eventlog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	redispkg "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func eventlogObject(values map[string]any) extension.Object {
	value, err := extension.FromJSON(values)
	if err != nil {
		panic(err)
	}
	object, ok := value.ObjectValue()
	if !ok {
		panic("eventlog test value is not object")
	}
	return object
}

func TestAppendPersistsBinaryEnvelope(t *testing.T) {
	db := &fakeDB{
		row: fakeRow{values: []any{int64(7), "evt_existing", time.Unix(10, 0).UTC()}},
	}
	envelope := events.Envelope{
		ID:              "evt_existing",
		EventType:       "media:upload:success",
		Payload:         eventlogObject(map[string]any{"media_id": "m1"}),
		PayloadEncoding: events.PayloadEncodingJSON,
		Metadata: eventlogObject(map[string]any{
			"correlation_id":  "corr_append",
			"organization_id": "org_1",
		}),
		CorrelationID: "corr_append",
		SchemaVersion: events.EnvelopeSchemaVersion,
		Timestamp:     time.Unix(9, 0).UTC(),
	}

	entry, err := Append(context.Background(), db, envelope)
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if entry.ID != 7 || entry.EventID != "evt_existing" || entry.OrganizationID != "org_1" {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	decoded, err := events.FromBinary(entry.Envelope)
	if err != nil {
		t.Fatalf("FromBinary() error = %v", err)
	}
	if decoded.ID != "evt_existing" || decoded.EventType != "media:upload:success" {
		t.Fatalf("decoded envelope = %+v", decoded)
	}
	if !strings.Contains(db.lastQuery, "INSERT INTO foundation_event_log") {
		t.Fatalf("append query did not target foundation_event_log: %s", db.lastQuery)
	}
}

func TestFetchPendingParsesStoredEnvelope(t *testing.T) {
	rawMetadata, err := json.Marshal(map[string]any{"correlation_id": "corr_fetch"})
	if err != nil {
		t.Fatal(err)
	}
	db := &fakeDB{
		rows: []fakeRow{pendingRowWithMetadata(2, "evt_fetch", "corr_fetch", []byte("envelope"), string(rawMetadata), 3, "redis unavailable")},
	}
	entries, err := FetchPending(context.Background(), db, 0, 0)
	if err != nil {
		t.Fatalf("FetchPending() error = %v", err)
	}
	if len(entries) != 1 || string(entries[0].Envelope) != "envelope" || entries[0].PublishAttempts != 3 {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestPublishPendingLeavesFailedEventUnpublished(t *testing.T) {
	db := &fakeDB{
		rows: []fakeRow{pendingRow(5, "evt_fail", "corr_1", []byte("envelope"))},
	}
	result, err := PublishPending(context.Background(), db, failingAppender{}, PublishOptions{Stream: "events"})
	if err == nil {
		t.Fatalf("expected publish error")
	}
	if result.Read != 1 || result.Failed != 1 || result.Published != 0 {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(db.execs[0], "publish_attempts = publish_attempts + 1") {
		t.Fatalf("failure did not mark retry state: %v", db.execs)
	}
}

func TestPublishPendingUsesBatchAppenderAndBatchMark(t *testing.T) {
	db := &fakeDB{
		rows: []fakeRow{
			pendingRow(10, "evt_batch_1", "corr_1", []byte("one")),
			pendingRow(11, "evt_batch_2", "corr_2", []byte("two")),
		},
	}
	appender := &recordingBatchAppender{ids: []string{"1-0", "2-0"}}
	result, err := PublishPending(context.Background(), db, appender, PublishOptions{Stream: "events"})
	if err != nil {
		t.Fatalf("PublishPending() error = %v", err)
	}
	if result.Read != 2 || result.Published != 2 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	if appender.batchCalls != 1 || len(appender.batchValues) != 2 {
		t.Fatalf("batch appender calls=%d values=%d", appender.batchCalls, len(appender.batchValues))
	}
	if len(db.execs) != 1 || !strings.Contains(db.execs[0], "UNNEST") {
		t.Fatalf("expected one batch mark query, got %#v", db.execs)
	}
}

func TestPublishPendingClaimsRowsBeforeBatchPublish(t *testing.T) {
	db := &fakeQueryDB{
		rows: []fakeRow{
			pendingRow(20, "evt_claim_1", "corr_1", []byte("one")),
			pendingRow(21, "evt_claim_2", "corr_2", []byte("two")),
		},
	}
	appender := &recordingBatchAppender{ids: []string{"1-0", "2-0"}}
	result, err := PublishPending(context.Background(), db, appender, PublishOptions{
		Stream:     "events",
		ClaimToken: "claim_test",
	})
	if err != nil {
		t.Fatalf("PublishPending() error = %v", err)
	}
	if result.Read != 2 || result.Published != 2 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(db.lastQuery, "FOR UPDATE SKIP LOCKED") {
		t.Fatalf("claim query did not use SKIP LOCKED: %s", db.lastQuery)
	}
	if !strings.Contains(db.lastQuery, "publish_claim_expires_at") {
		t.Fatalf("claim query did not set a lease: %s", db.lastQuery)
	}
	if len(db.execs) != 1 || !strings.Contains(db.execs[0], "publish_claim_token = NULL") {
		t.Fatalf("published mark did not clear claim: %#v", db.execs)
	}
	if !strings.Contains(db.execs[0], "event_log.publish_claim_token = $4") {
		t.Fatalf("published mark did not require ownership token: %s", db.execs[0])
	}
}

func pendingRow(id int64, eventID string, correlationID string, envelope []byte) fakeRow {
	return pendingRowWithMetadata(id, eventID, correlationID, envelope, `{}`, 0, "")
}

func pendingRowWithMetadata(id int64, eventID string, correlationID string, envelope []byte, metadataJSON string, attempts int64, lastError string) fakeRow {
	return fakeRow{values: []any{
		id,
		eventID,
		"orders:create:success",
		"org_1",
		correlationID,
		events.EnvelopeSchemaVersion,
		events.PayloadEncodingProtobuf,
		envelope,
		metadataJSON,
		time.Unix(11, 0).UTC(),
		time.Unix(12, 0).UTC(),
		attempts,
		lastError,
	}}
}

type fakeDB struct {
	row       fakeRow
	rows      []fakeRow
	lastQuery string
	execs     []string
}

func (f *fakeDB) Exec(_ context.Context, query string, _ ...any) error {
	f.execs = append(f.execs, query)
	return nil
}

func (f *fakeDB) QueryRow(_ context.Context, query string, _ ...any) database.RowScanner {
	f.lastQuery = query
	return f.row
}

func (f *fakeDB) Query(_ context.Context, query string, _ ...any) (database.Rows, error) {
	f.lastQuery = query
	return &fakeRows{rows: f.rows}, nil
}

type fakeQueryDB struct {
	fakeDB
	rows []fakeRow
}

func (f *fakeQueryDB) Query(_ context.Context, query string, _ ...any) (database.Rows, error) {
	f.lastQuery = query
	return &fakeRows{rows: f.rows}, nil
}

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		switch ptr := dest[i].(type) {
		case *int64:
			*ptr = r.values[i].(int64)
		case *string:
			*ptr = r.values[i].(string)
		case *time.Time:
			*ptr = r.values[i].(time.Time)
		case *[]byte:
			*ptr = append([]byte(nil), r.values[i].([]byte)...)
		}
	}
	return nil
}

type fakeRows struct {
	rows  []fakeRow
	index int
	err   error
}

func (r *fakeRows) Close() {}

func (r *fakeRows) Next() bool {
	if r.index >= len(r.rows) {
		return false
	}
	r.index++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	if r.index == 0 || r.index > len(r.rows) {
		return errors.New("scan without current row")
	}
	return r.rows[r.index-1].Scan(dest...)
}

func (r *fakeRows) Err() error {
	return r.err
}

type failingAppender struct{}

func (failingAppender) XAdd(context.Context, string, redispkg.Values) (string, error) {
	return "", errors.New("redis unavailable")
}

type recordingBatchAppender struct {
	ids         []string
	errs        []error
	batchCalls  int
	batchValues []redispkg.Values
}

func (a *recordingBatchAppender) XAdd(context.Context, string, redispkg.Values) (string, error) {
	return "", errors.New("single append should not be used")
}

func (a *recordingBatchAppender) XAddMany(_ context.Context, _ string, entries []redispkg.Values) ([]string, []error) {
	a.batchCalls++
	a.batchValues = append([]redispkg.Values(nil), entries...)
	ids := append([]string(nil), a.ids...)
	errs := append([]error(nil), a.errs...)
	for len(ids) < len(entries) {
		ids = append(ids, "")
	}
	for len(errs) < len(entries) {
		errs = append(errs, nil)
	}
	return ids[:len(entries)], errs[:len(entries)]
}
