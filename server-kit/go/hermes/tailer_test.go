package hermes

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	redispkg "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func TestTailerPollOnceAppliesAndAcksRedisStream(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "stream_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	client := redispkg.NewMemoryClient("test")
	ctx := t.Context()
	for i := range 2 {
		_, err := client.XAdd(ctx, "hermes:signals", map[string]any{
			"organization_id": "org_1",
			"record_id":       fmt.Sprintf("tick_%d", i),
			"bucket":          i,
			"version":         i + 1,
		})
		if err != nil {
			t.Fatalf("XAdd() error = %v", err)
		}
	}
	source, err := NewRedisStreamSource(client, "hermes:signals", "hermes", "node_1")
	if err != nil {
		t.Fatalf("NewRedisStreamSource() error = %v", err)
	}
	tailer, err := NewTailer(store, "stream_ticks", source, streamTestDecoder, TailerOptions{MaxBatch: 8})
	if err != nil {
		t.Fatalf("NewTailer() error = %v", err)
	}

	result, err := tailer.PollOnce(ctx)
	if err != nil {
		t.Fatalf("PollOnce() error = %v", err)
	}
	if result.Read != 2 || result.Decoded != 2 || result.Acked != 2 || result.Apply.Applied != 2 {
		t.Fatalf("PollOnce() result = %+v", result)
	}
	count, err := store.Count(ctx, "stream_ticks", Query{OrganizationID: "org_1"}, Fence{})
	if err != nil || count != 2 {
		t.Fatalf("Count() = %d err=%v", count, err)
	}
	result, err = tailer.PollOnce(ctx)
	if err != nil || result.Read != 0 {
		t.Fatalf("second PollOnce() result=%+v err=%v", result, err)
	}
}

func TestPayloadTailerPollOnceAppliesBinaryRedisStream(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "payload_stream_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	client := redispkg.NewMemoryClient("test")
	ctx := t.Context()
	_, err := client.XAdd(ctx, "hermes:payloads", map[string]any{
		"payload":        []byte{5, 'z'},
		"event_type":     "signals:ticks:success",
		"schema_version": "capnp.signals.ticks.v1",
		"version":        uint64(5),
	})
	if err != nil {
		t.Fatalf("XAdd() error = %v", err)
	}
	source, err := NewRedisStreamPayloadSource(client, "hermes:payloads", "hermes", "node_1", "payload")
	if err != nil {
		t.Fatalf("NewRedisStreamPayloadSource() error = %v", err)
	}
	tailer, err := NewPayloadTailer(store, "payload_stream_ticks", source, binaryTestDecoder, TailerOptions{MaxBatch: 8})
	if err != nil {
		t.Fatalf("NewPayloadTailer() error = %v", err)
	}

	result, err := tailer.PollOnce(ctx)
	if err != nil {
		t.Fatalf("PollOnce() error = %v", err)
	}
	if result.Read != 1 || result.Decoded != 1 || result.Acked != 1 || result.Apply.Applied != 1 {
		t.Fatalf("PollOnce() result = %+v", result)
	}
	count, err := store.Count(ctx, "payload_stream_ticks", Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"bucket": 5},
	}, Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Count() = %d err=%v", count, err)
	}
}

func TestRedisStreamPayloadSourceDefaultsSourceIDToMessageID(t *testing.T) {
	client := redispkg.NewMemoryClient("test")
	ctx := t.Context()
	id, err := client.XAdd(ctx, "hermes:payload-defaults", map[string]any{
		"payload": []byte{1, 2, 3},
		"version": float64(12),
	})
	if err != nil {
		t.Fatalf("XAdd() error = %v", err)
	}
	source, err := NewRedisStreamPayloadSource(client, "hermes:payload-defaults", "hermes", "node_1", "payload")
	if err != nil {
		t.Fatalf("NewRedisStreamPayloadSource() error = %v", err)
	}
	messages, err := source.ReadPayloads(ctx, 1)
	if err != nil || len(messages) != 1 {
		t.Fatalf("ReadPayloads() len=%d err=%v", len(messages), err)
	}
	payload := messages[0].Payload
	if payload.SourceID != id {
		t.Fatalf("SourceID = %q, want %q", payload.SourceID, id)
	}
	if payload.Version != 12 {
		t.Fatalf("Version = %d, want 12", payload.Version)
	}
}

func TestStoreApplyRecordsBatch(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "record_batch",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	records := []database.DomainRecord{
		testRecord("signals", "ticks", "org_1", "tick_1", map[string]any{"bucket": 1}),
		testRecord("signals", "ticks", "org_1", "tick_2", map[string]any{"bucket": 1}),
	}
	result, err := store.ApplyRecords(t.Context(), "record_batch", "batch", 100, records)
	if err != nil || result.Applied != 2 {
		t.Fatalf("ApplyRecords() result=%+v err=%v", result, err)
	}
	count, err := store.Count(t.Context(), "record_batch", Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"bucket": 1},
	}, Fence{})
	if err != nil || count != 2 {
		t.Fatalf("Count() = %d err=%v", count, err)
	}
}

func TestStoreApplyRecordPayloadsBinary(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "binary_payloads",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	payloads := []RecordPayload{{
		SourceID:      "stream_1",
		Version:       10,
		EventType:     "signals:ticks:success",
		SchemaVersion: "capnp.signals.ticks.v1",
		Payload:       []byte{3, 'a'},
	}}
	result, err := store.ApplyRecordPayloads(t.Context(), "binary_payloads", payloads, binaryTestDecoder)
	if err != nil || result.Applied != 1 {
		t.Fatalf("ApplyRecordPayloads() result=%+v err=%v", result, err)
	}
	count, err := store.Count(t.Context(), "binary_payloads", Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"bucket": 3},
	}, Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Count() = %d err=%v", count, err)
	}
}

func TestStoreApplyRecordPayloadEventsBatchDecoder(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "binary_payload_events",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    10,
		MaxBytes:      1 << 20,
	})
	payloads := []RecordPayload{{
		SourceID:      "stream_1",
		Version:       10,
		EventType:     "signals:ticks:success",
		SchemaVersion: "capnp.signals.ticks.v1",
		Payload:       []byte{4, 'a'},
	}}
	result, err := store.ApplyRecordPayloadEvents(t.Context(), "binary_payload_events", payloads, binaryTestEventDecoder)
	if err != nil || result.Applied != 1 {
		t.Fatalf("ApplyRecordPayloadEvents() result=%+v err=%v", result, err)
	}
	count, err := store.Count(t.Context(), "binary_payload_events", Query{
		OrganizationID: "org_1",
		Filters:        map[string]any{"bucket": 4},
	}, Fence{})
	if err != nil || count != 1 {
		t.Fatalf("Count() = %d err=%v", count, err)
	}
}

func TestSourceMessagePayload(t *testing.T) {
	payload := []byte{1, 2, 3}
	got, ok := SourceMessagePayload(SourceMessage{Values: map[string]any{"payload": payload}}, "payload")
	if !ok || string(got) != string(payload) {
		t.Fatalf("SourceMessagePayload bytes = %v ok=%v", got, ok)
	}
	got, ok = SourceMessagePayload(SourceMessage{Values: map[string]any{"payload": "abc"}}, "payload")
	if !ok || string(got) != "abc" {
		t.Fatalf("SourceMessagePayload string = %q ok=%v", string(got), ok)
	}
}

func TestStoreConcurrentApplyAndBorrowedRead(t *testing.T) {
	store := newTestStore(t, ProjectionSpec{
		Name:          "concurrent_ticks",
		Domain:        "signals",
		Collection:    "ticks",
		IndexedFields: []string{"bucket"},
		MaxRecords:    128,
		MaxBytes:      1 << 20,
	})
	ctx := t.Context()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 128 {
			_, err := store.Apply(ctx, "concurrent_ticks", Event{
				Operation: OperationUpsert,
				SourceID:  fmt.Sprintf("apply_%d", i),
				Version:   uint64(i + 1),
				Record:    testRecord("signals", "ticks", "org_1", fmt.Sprintf("tick_%d", i), map[string]any{"bucket": i % 4}),
			})
			if err != nil {
				t.Errorf("Apply() error = %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 128 {
			_, err := store.ForEachView(ctx, "concurrent_ticks", Query{
				OrganizationID: "org_1",
				Filters:        map[string]any{"bucket": 1},
				Limit:          8,
			}, Fence{}, func(view RecordView) error {
				if view.OrganizationID != "org_1" {
					t.Errorf("cross-tenant view: %+v", view)
				}
				return nil
			})
			if err != nil {
				t.Errorf("ForEachView() error = %v", err)
				return
			}
		}
	}()
	wg.Wait()
}

func streamTestDecoder(_ context.Context, message SourceMessage) ([]Event, error) {
	orgID, _ := message.Values["organization_id"].(string)
	recordID, _ := message.Values["record_id"].(string)
	return []Event{{
		Operation: OperationUpsert,
		Version:   uint64(intFromAny(message.Values["version"])),
		Record: testRecord("signals", "ticks", orgID, recordID, map[string]any{
			"bucket": intFromAny(message.Values["bucket"]),
		}),
	}}, nil
}

func binaryTestDecoder(_ context.Context, payload RecordPayload) (database.DomainRecord, error) {
	return testRecord("signals", "ticks", "org_1", "tick_binary", map[string]any{
		"bucket": int(payload.Payload[0]),
		"symbol": string(payload.Payload[1]),
	}), nil
}

func binaryTestEventDecoder(ctx context.Context, payloads []RecordPayload, events []Event) ([]Event, error) {
	for _, payload := range payloads {
		record, err := binaryTestDecoder(ctx, payload)
		if err != nil {
			return nil, err
		}
		events = append(events, Event{
			Operation: OperationUpsert,
			SourceID:  payload.SourceID,
			Version:   payload.Version,
			Record:    record,
		})
	}
	return events, nil
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}
