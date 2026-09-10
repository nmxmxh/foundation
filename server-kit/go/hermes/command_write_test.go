package hermes

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

type commandLog struct {
	mu      sync.Mutex
	batches [][]database.DomainRecord
	ops     []Operation
}

func (c *commandLog) record(_ context.Context, records []database.DomainRecord, op Operation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batches = append(c.batches, records)
	c.ops = append(c.ops, op)
}

func (c *commandLog) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.batches)
}

func newCommandStore(t *testing.T) (*ProjectedRuntimeStore, *commandLog) {
	t.Helper()
	log := &commandLog{}
	store, err := WrapRuntimeStore(database.NewMemoryDB(), RuntimeStoreOptions{
		MaxRecordsPerScope: 32, MaxBytesPerScope: 1 << 20,
		OnCommandWrite: log.record,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	return store, log
}

func seatRecord(id string) database.DomainRecord {
	return database.DomainRecord{
		Domain: "communication", Collection: "presence", OrganizationID: "org_1", RecordID: id,
		Data: testRecordData(map[string]any{"room_id": "r1"}),
	}
}

// A command is the thing worth telling other processes about.
func TestCommandWritesAreAnnounced(t *testing.T) {
	store, log := newCommandStore(t)
	ctx := t.Context()

	if _, err := store.UpsertRecord(ctx, seatRecord("seat_1")); err != nil {
		t.Fatalf("UpsertRecord() error = %v", err)
	}
	if _, err := store.UpsertRecords(ctx, []database.DomainRecord{seatRecord("seat_2"), seatRecord("seat_3")}); err != nil {
		t.Fatalf("UpsertRecords() error = %v", err)
	}
	if err := store.DeleteRecordWithFields(ctx, seatRecord("seat_1")); err != nil {
		t.Fatalf("DeleteRecordWithFields() error = %v", err)
	}

	if log.count() != 3 {
		t.Fatalf("announced %d command writes, want 3", log.count())
	}
	if log.ops[0] != OperationUpsert || log.ops[2] != OperationDelete {
		t.Fatalf("announced operations %v, want upsert then delete", log.ops)
	}
	if len(log.batches[1]) != 2 {
		t.Fatalf("batch upsert announced %d records, want 2", len(log.batches[1]))
	}
}

// The sweeper's writes are this process catching up with news somebody else
// already announced. Re-announcing them turns a fan-out lane into an echo
// chamber whose traffic grows with the square of the fleet, so this is the
// invariant the whole convergence design rests on.
func TestSweeperWritesAreNotAnnounced(t *testing.T) {
	store, log := newCommandStore(t)
	ctx := t.Context()
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{BatchSize: 4})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	served := false
	if err := sweeper.AddSource("presence", func(_ context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		if served || !at.After(cursor) {
			return nil
		}
		served = true
		for _, id := range []string{"seat_a", "seat_b"} {
			if err := visit(seatRecord(id), at); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("AddSource() error = %v", err)
	}
	if err := sweeper.AddDeleteRecordSource("presence_tombstones", func(_ context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		if !at.After(cursor) {
			return nil
		}
		return visit(seatRecord("seat_gone"), at)
	}); err != nil {
		t.Fatalf("AddDeleteRecordSource() error = %v", err)
	}

	if _, err := sweeper.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if _, err := sweeper.SweepSource(ctx, "presence"); err != nil {
		t.Fatalf("SweepSource() error = %v", err)
	}

	if got := log.count(); got != 0 {
		t.Fatalf("sweeper announced %d command writes, want 0 — convergence would echo", got)
	}
}

// A store with no hook configured must behave exactly as before.
func TestCommandWriteHookIsOptional(t *testing.T) {
	store, err := WrapRuntimeStore(database.NewMemoryDB(),
		RuntimeStoreOptions{MaxRecordsPerScope: 8, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	if _, err := store.UpsertRecord(t.Context(), seatRecord("seat_1")); err != nil {
		t.Fatalf("UpsertRecord() error = %v", err)
	}
	if err := store.DeleteRecordWithFields(t.Context(), seatRecord("seat_1")); err != nil {
		t.Fatalf("DeleteRecordWithFields() error = %v", err)
	}
}
