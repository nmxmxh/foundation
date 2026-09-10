package hermes

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
)

// signalNode is one "process" on the lane: its own store, its own sweeper, its
// own identity, sharing a bus with the others.
type signalNode struct {
	store   *ProjectedRuntimeStore
	sweeper *MirrorSweeper
	signal  *ScopeSignal
	source  *tableSource
}

// tableSource stands in for a normalized table swept by updated_at: writes land
// in it, and a sweep reads whatever is newer than the cursor.
type tableSource struct {
	mu    sync.Mutex
	rows  map[string]database.DomainRecord
	stamp map[string]time.Time
	polls atomic.Int64
	clock time.Time
}

func newTableSource() *tableSource {
	return &tableSource{
		rows:  map[string]database.DomainRecord{},
		stamp: map[string]time.Time{},
		clock: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
	}
}

func (t *tableSource) write(rec database.DomainRecord) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.clock = t.clock.Add(time.Millisecond)
	t.rows[rec.RecordID] = rec
	t.stamp[rec.RecordID] = t.clock
}

func (t *tableSource) changed() ChangedSince {
	return func(_ context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		t.polls.Add(1)
		t.mu.Lock()
		type row struct {
			rec database.DomainRecord
			at  time.Time
		}
		pending := make([]row, 0, len(t.rows))
		for id, rec := range t.rows {
			if at := t.stamp[id]; at.After(cursor) {
				pending = append(pending, row{rec: rec, at: at})
			}
		}
		t.mu.Unlock()
		for _, r := range pending {
			if err := visit(r.rec, r.at); err != nil {
				return err
			}
		}
		return nil
	}
}

func newSignalNode(t *testing.T, bus events.Bus) *signalNode {
	t.Helper()
	signal, err := NewScopeSignal(ScopeSignalOptions{Bus: bus})
	if err != nil {
		t.Fatalf("NewScopeSignal() error = %v", err)
	}
	t.Cleanup(func() { _ = signal.Close() })

	store, err := WrapRuntimeStore(database.NewMemoryDB(), RuntimeStoreOptions{
		MaxRecordsPerScope: 64, MaxBytesPerScope: 1 << 20,
		OnCommandWrite: signal.OnCommandWrite,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{BatchSize: 8, Interval: time.Hour})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	source := newTableSource()
	if err := sweeper.AddScopeSource("communication", "presence", source.changed()); err != nil {
		t.Fatalf("AddScopeSource() error = %v", err)
	}
	if err := signal.Converge(sweeper); err != nil {
		t.Fatalf("Converge() error = %v", err)
	}
	return &signalNode{store: store, sweeper: sweeper, signal: signal, source: source}
}

func presenceRecord(id string) database.DomainRecord {
	return database.DomainRecord{
		Domain: "communication", Collection: "presence", OrganizationID: "org_1", RecordID: id,
		Data: testRecordData(map[string]any{"room_id": "r1"}),
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The property the whole lane exists for: a write on one process becomes
// visible on another without either of them waiting for a timer. The sweeper's
// interval here is an hour, so a pass on the reconcile clock cannot be what
// converged it.
func TestScopeSignalConvergesAnotherProcessWrite(t *testing.T) {
	bus := events.NewInMemoryBus(64)
	writer := newSignalNode(t, bus)
	reader := newSignalNode(t, bus)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = reader.sweeper.Run(ctx) }()
	waitFor(t, "reader's first pass", func() bool { return reader.source.polls.Load() > 0 })

	rec := presenceRecord("seat_1")
	reader.source.write(rec) // the writer's table row, as the reader would read it
	if _, err := writer.store.UpsertRecord(ctx, rec); err != nil {
		t.Fatalf("UpsertRecord() error = %v", err)
	}

	waitFor(t, "the reader to converge the write", func() bool {
		_, found, err := reader.store.GetRecord(ctx, "communication", "presence", "org_1", "seat_1")
		return err == nil && found
	})
	if got := reader.sweeper.Stats().Signalled; got == 0 {
		t.Fatal("reader converged with no signalled sweeps: it swept on the timer, not the signal")
	}
}

// A process must not converge on its own announcement. The bus hands a
// publisher its own envelope on the way out, and acting on it means every write
// pays for a redundant read of the scope it just wrote.
func TestScopeSignalSkipsItsOwnAnnouncement(t *testing.T) {
	bus := events.NewInMemoryBus(64)
	node := newSignalNode(t, bus)

	if _, err := node.store.UpsertRecord(t.Context(), presenceRecord("seat_1")); err != nil {
		t.Fatalf("UpsertRecord() error = %v", err)
	}
	waitFor(t, "the announcement to be published", func() bool { return node.signal.Stats().Announced == 1 })
	waitFor(t, "the echo to be counted", func() bool { return node.signal.Stats().Echoes == 1 })

	if got := node.signal.Stats().Received; got != 0 {
		t.Fatalf("Received = %d, want 0: the process acted on its own announcement", got)
	}
	if got := node.sweeper.Stats().Signalled; got != 0 {
		t.Fatalf("Signalled = %d, want 0: a self-announcement caused a sweep", got)
	}
}

// The invariant the fan-out rests on, through the whole lane rather than at the
// store: what a sweeper converges must not be announced again. A replica that
// rebroadcast what it converged would make fleet traffic grow with the square
// of the fleet.
func TestScopeSignalDoesNotAnnounceConvergedWrites(t *testing.T) {
	bus := events.NewInMemoryBus(64)
	writer := newSignalNode(t, bus)
	reader := newSignalNode(t, bus)

	rec := presenceRecord("seat_1")
	reader.source.write(rec)
	if _, err := writer.store.UpsertRecord(t.Context(), rec); err != nil {
		t.Fatalf("UpsertRecord() error = %v", err)
	}
	waitFor(t, "the reader to be notified", func() bool { return reader.signal.Stats().Received == 1 })

	if _, err := reader.sweeper.SweepSource(t.Context(), ScopeSourceName("communication", "presence")); err != nil {
		t.Fatalf("SweepSource() error = %v", err)
	}
	// Give a stray announcement time to reach the publisher before concluding
	// there was none.
	time.Sleep(50 * time.Millisecond)
	if got := reader.signal.Stats().Announced; got != 0 {
		t.Fatalf("the converging process announced %d times, want 0 — the lane echoes", got)
	}
}

// Announce and converge must agree on the name of every source, and the way to
// guarantee that is to derive both from the scope rather than to write the name
// down twice.
func TestScopeSourceNamesMatchRegisteredSources(t *testing.T) {
	store, err := WrapRuntimeStore(database.NewMemoryDB(),
		RuntimeStoreOptions{MaxRecordsPerScope: 8, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	if err := sweeper.AddScopeSource("communication", "presence", newTableSource().changed()); err != nil {
		t.Fatalf("AddScopeSource() error = %v", err)
	}
	if err := sweeper.AddScopeDeleteRecordSource("communication", "presence",
		func(context.Context, time.Time, func(database.DomainRecord, time.Time) error) error { return nil }); err != nil {
		t.Fatalf("AddScopeDeleteRecordSource() error = %v", err)
	}

	rec := presenceRecord("seat_1")
	for _, tc := range []struct {
		op   Operation
		want string
	}{
		{OperationUpsert, ScopeSourceName("communication", "presence")},
		{OperationDelete, ScopeDeleteSourceName("communication", "presence")},
	} {
		name := DefaultScopeSourceName(rec, tc.op)
		if name != tc.want {
			t.Fatalf("DefaultScopeSourceName(%s) = %q, want %q", tc.op, name, tc.want)
		}
		if err := sweeper.Notify(name); err != nil {
			t.Fatalf("Notify(%q) after AddScope*Source: %v", name, err)
		}
	}
}

// A scope nobody registered is a wiring mistake, and the lane must not spend
// bus traffic on it or let it pass for convergence.
func TestScopeSignalCountsUnroutableScopes(t *testing.T) {
	bus := events.NewInMemoryBus(64)
	node := newSignalNode(t, bus)

	if _, err := node.store.UpsertRecord(t.Context(), database.DomainRecord{
		Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: "dish_1",
		Data: testRecordData(map[string]any{"name": "jollof"}),
	}); err != nil {
		t.Fatalf("UpsertRecord() error = %v", err)
	}
	waitFor(t, "the unroutable scope to be counted", func() bool { return node.signal.Stats().Unroutable == 1 })
	if got := node.signal.Stats().Announced; got != 0 {
		t.Fatalf("Announced = %d, want 0: an unroutable scope reached the bus", got)
	}
}

// A store with no signal wired must behave exactly as before.
func TestScopeSignalRequiresABus(t *testing.T) {
	if _, err := NewScopeSignal(ScopeSignalOptions{}); err == nil {
		t.Fatal("NewScopeSignal() with no bus returned no error")
	}
}

// Shutdown races a write in every deployment that stops under load: a request
// commits, its announcement is on the way to the queue, and Close runs. Checking
// a flag and then sending would be a send on a closed channel.
func TestScopeSignalCloseRacesAWrite(t *testing.T) {
	bus := events.NewInMemoryBus(64)
	signal, err := NewScopeSignal(ScopeSignalOptions{Bus: bus, QueueSize: 1})
	if err != nil {
		t.Fatalf("NewScopeSignal() error = %v", err)
	}
	store, err := WrapRuntimeStore(database.NewMemoryDB(), RuntimeStoreOptions{
		MaxRecordsPerScope: 64, MaxBytesPerScope: 1 << 20,
		OnCommandWrite: signal.OnCommandWrite,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}

	var writers sync.WaitGroup
	start := make(chan struct{})
	for i := range 8 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			for j := range 50 {
				if _, err := store.UpsertRecord(t.Context(), presenceRecord(
					"seat_"+string(rune('a'+i))+string(rune('a'+j%26)))); err != nil {
					t.Errorf("UpsertRecord() error = %v", err)
					return
				}
			}
		}()
	}
	close(start)
	time.Sleep(time.Millisecond)
	if err := signal.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	writers.Wait()

	// Close twice must also be safe.
	if err := signal.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

// gatedBus holds each Publish open until the test lets it go, so a burst can be
// made to arrive while an announcement is genuinely in flight. Without the gate
// the publisher drains an in-process bus faster than a test can fill it, and
// the test would pass or fail on scheduling.
type gatedBus struct {
	inner   *events.InMemoryBus
	release chan struct{}
	entered chan struct{}
}

func newGatedBus() *gatedBus {
	return &gatedBus{
		inner:   events.NewInMemoryBus(1024),
		release: make(chan struct{}),
		entered: make(chan struct{}, 64),
	}
}

func (g *gatedBus) Publish(ctx context.Context, envelope events.Envelope) error {
	g.entered <- struct{}{}
	<-g.release
	return g.inner.Publish(ctx, envelope)
}

func (g *gatedBus) Subscribe(pattern string, subscriber events.Subscriber) {
	g.inner.Subscribe(pattern, subscriber)
}

func (g *gatedBus) Recent(limit int) []events.Envelope { return g.inner.Recent(limit) }

// A burst on one scope must cost one more announcement, not one per write.
// Every replica in the fleet is listening to this channel, so an announcement
// per write puts the whole fleet's bus traffic on the write rate — and each
// copy says exactly what the first one already said, because the hint carries
// no data.
func TestScopeSignalCoalescesABurstOnOneScope(t *testing.T) {
	bus := newGatedBus()
	signal, err := NewScopeSignal(ScopeSignalOptions{Bus: bus, QueueSize: 16})
	if err != nil {
		t.Fatalf("NewScopeSignal() error = %v", err)
	}
	// Ordered so the gate opens before Close waits on the publisher: a failed
	// assertion would otherwise leave the publisher blocked inside Publish and
	// turn a clear message into a test timeout.
	defer func() { _ = signal.Close() }()
	defer close(bus.release)
	store, err := WrapRuntimeStore(database.NewMemoryDB(), RuntimeStoreOptions{
		MaxRecordsPerScope: 512, MaxBytesPerScope: 1 << 20,
		OnCommandWrite: signal.OnCommandWrite,
	})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}

	// One write gets the publisher into Publish and stuck there.
	if _, err := store.UpsertRecord(t.Context(), presenceRecord("seat_0")); err != nil {
		t.Fatalf("UpsertRecord() error = %v", err)
	}
	<-bus.entered

	// 500 more writes to the same scope, all while that announcement is in
	// flight. Exactly one of them may queue behind it.
	const burst = 500
	for i := range burst {
		if _, err := store.UpsertRecord(t.Context(), presenceRecord("seat_"+strconv.Itoa(i))); err != nil {
			t.Fatalf("UpsertRecord() error = %v", err)
		}
	}
	stats := signal.Stats()
	if stats.Coalesced != burst-1 {
		t.Fatalf("Coalesced = %d, want %d: the queue held duplicate announcements", stats.Coalesced, burst-1)
	}
	if stats.Dropped != 0 {
		t.Fatalf("Dropped = %d, want 0: duplicates filled a 16-slot queue", stats.Dropped)
	}

	// Let both go: 501 writes bought two announcements.
	bus.release <- struct{}{}
	<-bus.entered
	bus.release <- struct{}{}
	waitFor(t, "both announcements to publish", func() bool { return signal.Stats().Announced == 2 })

	select {
	case <-bus.entered:
		t.Fatal("a third announcement was published: the burst did not coalesce")
	case <-time.After(100 * time.Millisecond):
	}
}

// Distinct scopes are not duplicates: folding them together would lose one.
func TestScopeSignalDoesNotCoalesceDistinctScopes(t *testing.T) {
	bus := newGatedBus()
	signal, err := NewScopeSignal(ScopeSignalOptions{Bus: bus, QueueSize: 16})
	if err != nil {
		t.Fatalf("NewScopeSignal() error = %v", err)
	}
	defer func() { _ = signal.Close() }()
	defer close(bus.release)

	rec := presenceRecord("seat_1")
	signal.OnCommandWrite(t.Context(), []database.DomainRecord{rec}, OperationUpsert)
	<-bus.entered // the upsert announcement is in flight
	signal.OnCommandWrite(t.Context(), []database.DomainRecord{rec}, OperationDelete)

	if got := signal.Stats().Coalesced; got != 0 {
		t.Fatalf("Coalesced = %d, want 0: a delete was folded into an upsert announcement", got)
	}
	bus.release <- struct{}{}
	<-bus.entered
	bus.release <- struct{}{}
	waitFor(t, "both scopes to announce", func() bool { return signal.Stats().Announced == 2 })
}
