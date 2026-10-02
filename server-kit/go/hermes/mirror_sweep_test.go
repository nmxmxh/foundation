package hermes

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// countingSource records how often it was polled, so a test can prove a
// targeted sweep did not touch the other sources.
type countingSource struct {
	mu     sync.Mutex
	polls  int
	record database.DomainRecord
	at     time.Time
	served bool
}

func (c *countingSource) changed() ChangedSince {
	return func(_ context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		c.mu.Lock()
		c.polls++
		alreadyServed := c.served
		c.served = true
		c.mu.Unlock()
		if alreadyServed || !c.at.After(cursor) {
			return nil
		}
		return visit(c.record, c.at)
	}
}

func (c *countingSource) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.polls
}

func newSweeperForSourceTest(t *testing.T) (*MirrorSweeper, *ProjectedRuntimeStore) {
	t.Helper()
	store, err := WrapRuntimeStore(database.NewMemoryDB(),
		RuntimeStoreOptions{MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{BatchSize: 4})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	return sweeper, store
}

// A caller that already knows which scope changed should not pay to poll every
// other source to act on it. That is the whole reason SweepSource exists: with
// a change signal arriving out of band, SweepOnce would turn one event into one
// query per registered source.
func TestSweepSourcePollsOnlyTheNamedSource(t *testing.T) {
	sweeper, _ := newSweeperForSourceTest(t)
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	wanted := &countingSource{at: at, record: database.DomainRecord{
		Domain: "communication", Collection: "presence", OrganizationID: "org_1", RecordID: "seat_1",
		Data: testRecordData(map[string]any{"room_id": "r1"}),
	}}
	other := &countingSource{at: at, record: database.DomainRecord{
		Domain: "community", Collection: "participants", OrganizationID: "org_1", RecordID: "par_1",
		Data: testRecordData(map[string]any{"audience": "members"}),
	}}
	if err := sweeper.AddSource("presence", wanted.changed()); err != nil {
		t.Fatalf("AddSource(presence) error = %v", err)
	}
	if err := sweeper.AddSource("participants", other.changed()); err != nil {
		t.Fatalf("AddSource(participants) error = %v", err)
	}

	swept, err := sweeper.SweepSource(t.Context(), "presence")
	if err != nil {
		t.Fatalf("SweepSource() error = %v", err)
	}
	if swept != 1 {
		t.Fatalf("SweepSource() swept %d records, want 1", swept)
	}
	if wanted.count() != 1 {
		t.Fatalf("named source polled %d times, want 1", wanted.count())
	}
	if other.count() != 0 {
		t.Fatalf("unnamed source polled %d times, want 0", other.count())
	}
}

// The cursor is per source state, and signal-driven sweeps arrive whenever the
// writer commits. Two of them for the same source must not interleave on it.
func TestSweepSourceIsSafeConcurrently(t *testing.T) {
	sweeper, _ := newSweeperForSourceTest(t)
	source := &countingSource{
		at: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		record: database.DomainRecord{
			Domain: "communication", Collection: "presence", OrganizationID: "org_1", RecordID: "seat_1",
			Data: testRecordData(map[string]any{"room_id": "r1"}),
		},
	}
	if err := sweeper.AddSource("presence", source.changed()); err != nil {
		t.Fatalf("AddSource() error = %v", err)
	}

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := sweeper.SweepSource(t.Context(), "presence"); err != nil {
				t.Errorf("SweepSource() error = %v", err)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := sweeper.SweepOnce(t.Context()); err != nil {
				t.Errorf("SweepOnce() error = %v", err)
			}
		}()
	}
	wg.Wait()

	if got := sweeper.Stats().Swept; got != 1 {
		t.Fatalf("Stats().Swept = %d, want 1: the record converged more than once", got)
	}
}

// An unknown name is a wiring mistake, not a silent no-op: a signal naming a
// source nobody registered would otherwise look exactly like convergence.
func TestSweepSourceRejectsUnknownName(t *testing.T) {
	sweeper, _ := newSweeperForSourceTest(t)
	if err := sweeper.AddSource("presence", (&countingSource{}).changed()); err != nil {
		t.Fatalf("AddSource() error = %v", err)
	}
	if _, err := sweeper.SweepSource(t.Context(), "participants"); err == nil {
		t.Fatal("SweepSource() with an unregistered name returned no error")
	}
	if names := sweeper.SourceNames(); len(names) != 1 || names[0] != "presence" {
		t.Fatalf("SourceNames() = %v, want [presence]", names)
	}
}

// gatedSource turns each poll into a rendezvous, so a test can hold one sweep
// open while signals arrive and count exactly what the sweeper did.
type gatedSource struct {
	polls   atomic.Int64
	enter   chan struct{}
	release chan struct{}
}

func newGatedSource() *gatedSource {
	return &gatedSource{
		enter:   make(chan struct{}, 8),
		release: make(chan struct{}, 8),
	}
}

func (g *gatedSource) changed() ChangedSince {
	return func(context.Context, time.Time, func(database.DomainRecord, time.Time) error) error {
		g.polls.Add(1)
		g.enter <- struct{}{}
		<-g.release
		return nil
	}
}

// pass admits one poll and lets it finish.
func (g *gatedSource) pass(t *testing.T, what string) {
	t.Helper()
	select {
	case <-g.enter:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	g.release <- struct{}{}
}

// A burst is the case that decides whether this lane scales: every replica
// receives every announcement, so a signal that costs a query is a query per
// write per process. Signals arriving while a sweep runs must collapse into the
// one sweep that follows it, because that sweep reads everything past the
// cursor anyway.
func TestNotifyCoalescesSignalsIntoOneSweep(t *testing.T) {
	sweeper, _ := newSweeperForSourceTest(t)
	source := newGatedSource()
	if err := sweeper.AddSource("presence", source.changed()); err != nil {
		t.Fatalf("AddSource() error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _ = sweeper.Run(ctx) }()

	// Poll 1 is Run's opening full pass; it leaves the loop waiting.
	source.pass(t, "the opening reconcile pass")

	// A burst arrives while nothing is sweeping: the first signal starts a
	// sweep, the rest land on the flag it set.
	for range 50 {
		if err := sweeper.Notify("presence"); err != nil {
			t.Fatalf("Notify() error = %v", err)
		}
	}
	select {
	case <-source.enter: // poll 2, now held open
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the signalled sweep")
	}

	// A second burst arrives while that sweep is in flight. It cannot be
	// covered by a pass that already read, so it must survive as one more.
	for range 50 {
		if err := sweeper.Notify("presence"); err != nil {
			t.Fatalf("Notify() error = %v", err)
		}
	}
	source.release <- struct{}{}
	source.pass(t, "the sweep covering the second burst")

	// Nothing further: 100 signals bought two sweeps.
	time.Sleep(100 * time.Millisecond)
	if got := source.polls.Load(); got != 3 {
		t.Fatalf("source polled %d times, want 3 (one reconcile + two signalled)", got)
	}
	stats := sweeper.Stats()
	if stats.Signalled != 2 {
		t.Fatalf("Stats().Signalled = %d, want 2", stats.Signalled)
	}
	if stats.Coalesced != 98 {
		t.Fatalf("Stats().Coalesced = %d, want 98", stats.Coalesced)
	}
	cancel()
	<-done
}

// Notify must never block the caller: it runs on whatever goroutine the bus
// dispatches from, in front of every other event that process was about to
// handle.
func TestNotifyDoesNotBlockOnAnInFlightSweep(t *testing.T) {
	sweeper, _ := newSweeperForSourceTest(t)
	source := newGatedSource()
	if err := sweeper.AddSource("presence", source.changed()); err != nil {
		t.Fatalf("AddSource() error = %v", err)
	}

	held := make(chan struct{})
	go func() {
		defer close(held)
		_, _ = sweeper.SweepSource(t.Context(), "presence")
	}()
	<-source.enter // the sweep is inside the source, holding the source lock

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		for range 1000 {
			if err := sweeper.Notify("presence"); err != nil {
				t.Errorf("Notify() error = %v", err)
				return
			}
		}
	}()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("Notify blocked behind an in-flight sweep")
	}
	source.release <- struct{}{}
	<-held
}

// A signal naming a source nobody registered converges nothing while every part
// looks correct alone, so it has to be loud.
func TestNotifyRejectsUnknownSource(t *testing.T) {
	sweeper, _ := newSweeperForSourceTest(t)
	if err := sweeper.AddSource("presence", (&countingSource{}).changed()); err != nil {
		t.Fatalf("AddSource() error = %v", err)
	}
	if err := sweeper.Notify("participants"); err == nil {
		t.Fatal("Notify() with an unregistered name returned no error")
	}
	if got := sweeper.Stats().Unroutable; got != 1 {
		t.Fatalf("Stats().Unroutable = %d, want 1", got)
	}
}

// Two sources under one name make the second unreachable, and a signal naming
// it converges the first instead — which looks exactly like convergence.
func TestRegisteringADuplicateSourceNameFails(t *testing.T) {
	sweeper, _ := newSweeperForSourceTest(t)
	if err := sweeper.AddSource("presence", (&countingSource{}).changed()); err != nil {
		t.Fatalf("AddSource() error = %v", err)
	}
	if err := sweeper.AddDeleteSource("presence",
		func(context.Context, time.Time, func(string, string, string, string, time.Time) error) error {
			return nil
		}); err == nil {
		t.Fatal("AddDeleteSource() reused a registered name without error")
	}
}

// TestMirrorSweeperDeleteRecordSourceCarriesAudienceFields pins the addressed
// delete lane end to end through the sweeper: a DeletedRecordsSince source
// streams the deleted row's audience-bearing fields, the sweep projects the
// delete carrying them, and the observer — which is what the projection gateway
// subscribes to — sees a delete it can address. An identity-only DeleteSince
// source is unchanged and yields a delete with no fields, which is exactly the
// difference the two registration methods exist to express.

func TestMirrorSweeperDeleteRecordSourceCarriesAudienceFields(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	seed := func(recordID string) database.DomainRecord {
		return database.DomainRecord{
			Domain: "marketplace", Collection: "orders", OrganizationID: "org_1", RecordID: recordID,
			Data: testRecordData(map[string]any{"customer_id": "cust_alice"}),
		}
	}
	for _, id := range []string{"order_addressed", "order_anonymous"} {
		if _, upsertErr := store.UpsertRecord(ctx, seed(id)); upsertErr != nil {
			t.Fatalf("UpsertRecord(%s) error = %v", id, upsertErr)
		}
	}

	var deletes []AppliedMutation
	cancel := store.Store().Observe(func(_ string, mutations []AppliedMutation) {
		for _, mutation := range mutations {
			if mutation.Operation == OperationDelete {
				deletes = append(deletes, mutation)
			}
		}
	})
	defer cancel()

	deletedAt := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	addressed, anonymous := tombstoneSources(deletedAt)

	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{BatchSize: 4})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	if err := sweeper.AddDeleteRecordSource("orders_tombstones", addressed); err != nil {
		t.Fatalf("AddDeleteRecordSource() error = %v", err)
	}
	if err := sweeper.AddDeleteSource("orders_legacy_tombstones", anonymous); err != nil {
		t.Fatalf("AddDeleteSource() error = %v", err)
	}

	if n, err := sweeper.SweepOnce(ctx); err != nil || n != 2 {
		t.Fatalf("SweepOnce() = %d err=%v, want 2", n, err)
	}
	// The cursor advances for both source kinds, so a swept deletion is not
	// re-applied on the next pass.
	if n, err := sweeper.SweepOnce(ctx); err != nil || n != 0 {
		t.Fatalf("idle SweepOnce() = %d err=%v, want 0", n, err)
	}

	assertDeleteAudience(t, deletes)

	// Both rows are gone from the durable mirror regardless of addressing.
	for _, id := range []string{"order_addressed", "order_anonymous"} {
		if _, found, getErr := base.GetRecord(ctx, "marketplace", "orders", "org_1", id); getErr != nil || found {
			t.Fatalf("mirror still holds %s: found=%v err=%v", id, found, getErr)
		}
	}
}

// assertDeleteAudience requires that the record-bearing source produced a
// tombstone carrying the audience field, and the identity-only source one
// carrying nothing — the whole difference between the two registrations.

func assertDeleteAudience(t *testing.T, deletes []AppliedMutation) {
	t.Helper()
	byRecord := map[string]database.RecordData{}
	for _, mutation := range deletes {
		byRecord[mutation.Record.RecordID] = mutation.Record.Data
	}
	if len(byRecord) != 2 {
		t.Fatalf("observed deletes for %v, want both records", byRecord)
	}
	addressed, ok := byRecord["order_addressed"]
	if !ok {
		t.Fatal("no delete observed for order_addressed")
	}
	value, found := addressed.Get("customer_id")
	if !found || !value.Equal(database.StringValue("cust_alice")) {
		t.Fatalf("addressed delete carried %v, want customer_id=cust_alice", addressed)
	}
	if anonymous := byRecord["order_anonymous"]; len(anonymous) != 0 {
		t.Fatalf("identity-only delete carried %v, want no fields", anonymous)
	}
}

// tombstoneSources returns one delete source of each kind over the same
// deletion instant: the record-bearing one carries the audience field, the
// identity-only one cannot.

func tombstoneSources(deletedAt time.Time) (DeletedRecordsSince, DeletedSince) {
	addressed := func(_ context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		if !deletedAt.After(cursor) {
			return nil
		}
		// The tombstone carries only the audience field, not a copy of the row.
		return visit(database.DomainRecord{
			Domain: "marketplace", Collection: "orders", OrganizationID: "org_1", RecordID: "order_addressed",
			Data: testRecordData(map[string]any{"customer_id": "cust_alice"}),
		}, deletedAt)
	}
	anonymous := func(_ context.Context, cursor time.Time, visit func(string, string, string, string, time.Time) error) error {
		if !deletedAt.After(cursor) {
			return nil
		}
		return visit("marketplace", "orders", "org_1", "order_anonymous", deletedAt)
	}
	return addressed, anonymous
}

func TestMirrorSweeperPushesChangedRows(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()

	// A fake source table: rows with updated_at, served incrementally.
	type row struct {
		id string
		at time.Time
	}
	t0 := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	rows := []row{{"dish_1", t0}, {"dish_2", t0.Add(time.Second)}}
	polls := 0
	source := func(_ context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		polls++
		for _, r := range rows {
			if !r.at.After(cursor) {
				continue
			}
			if err := visit(database.DomainRecord{
				Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: r.id,
				Data: testRecordData(map[string]any{"state": "published"}),
			}, r.at); err != nil {
				return err
			}
		}
		return nil
	}
	failing := func(context.Context, time.Time, func(database.DomainRecord, time.Time) error) error {
		return errors.New("source down")
	}

	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{BatchSize: 1})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	if err := sweeper.AddSource("menu_dishes", source); err != nil {
		t.Fatalf("AddSource() error = %v", err)
	}
	if err := sweeper.AddSource("broken", failing); err != nil {
		t.Fatalf("AddSource(broken) error = %v", err)
	}

	// First sweep from zero cursor = full sync.
	n, err := sweeper.SweepOnce(ctx)
	if err != nil || n != 2 {
		t.Fatalf("SweepOnce() = %d err=%v, want 2", n, err)
	}
	if _, found, err := base.GetRecord(ctx, "menu", "dishes", "org_1", "dish_2"); err != nil || !found {
		t.Fatalf("mirror after sweep: found=%v err=%v", found, err)
	}
	name := store.ProjectionName("menu", "dishes", "org_1")
	if count, _ := store.Store().Count(ctx, name, Query{OrganizationID: "org_1"}, Fence{}); count != 2 {
		t.Fatalf("hot count = %d, want 2", count)
	}

	// Second sweep: cursor advanced, nothing re-read.
	if n, _ := sweeper.SweepOnce(ctx); n != 0 {
		t.Fatalf("idle sweep swept %d, want 0", n)
	}

	// A change after the cursor is picked up.
	rows = append(rows, row{"dish_3", t0.Add(2 * time.Second)})
	if n, _ := sweeper.SweepOnce(ctx); n != 1 {
		t.Fatalf("incremental sweep swept %d, want 1", n)
	}

	stats := sweeper.Stats()
	if stats.Swept != 3 || stats.Errors != 3 {
		t.Fatalf("stats = %+v, want Swept=3 Errors=3 (one failing source per pass)", stats)
	}
}

// TestMirrorSweeperConvergesHardDeletes pins the delete lane: identities
// announced by a tombstone source are removed from the mirror and the hot
// partition (with live fan-out via the store observer), the cursor advances,
// and replays are safe because DeleteRecord is idempotent.

func TestMirrorSweeperConvergesHardDeletes(t *testing.T) {
	base := database.NewMemoryDB()
	store, err := WrapRuntimeStore(base, RuntimeStoreOptions{MaxRecordsPerScope: 16, MaxBytesPerScope: 1 << 20})
	if err != nil {
		t.Fatalf("WrapRuntimeStore() error = %v", err)
	}
	ctx := t.Context()
	for _, id := range []string{"dish_1", "dish_2"} {
		if _, err := store.UpsertRecord(ctx, database.DomainRecord{
			Domain: "menu", Collection: "dishes", OrganizationID: "org_1", RecordID: id,
			Data: testRecordData(map[string]any{"state": "published"}),
		}); err != nil {
			t.Fatalf("seed %s error = %v", id, err)
		}
	}

	t0 := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	type tomb struct {
		id string
		at time.Time
	}
	tombs := []tomb{}
	source := func(_ context.Context, cursor time.Time, visit func(string, string, string, string, time.Time) error) error {
		for _, tb := range tombs {
			if !tb.at.After(cursor) {
				continue
			}
			if err := visit("menu", "dishes", "org_1", tb.id, tb.at); err != nil {
				return err
			}
		}
		return nil
	}
	sweeper, err := NewMirrorSweeper(store, MirrorSweepOptions{})
	if err != nil {
		t.Fatalf("NewMirrorSweeper() error = %v", err)
	}
	if err := sweeper.AddDeleteSource("tombstones", source); err != nil {
		t.Fatalf("AddDeleteSource() error = %v", err)
	}

	// Hard delete announced by tombstone: swept away from mirror + hot.
	tombs = append(tombs, tomb{"dish_1", t0})
	if n, err := sweeper.SweepOnce(ctx); err != nil || n != 1 {
		t.Fatalf("SweepOnce() = %d err=%v, want 1", n, err)
	}
	if _, found, _ := base.GetRecord(ctx, "menu", "dishes", "org_1", "dish_1"); found {
		t.Fatal("mirror row survived delete sweep")
	}
	name := store.ProjectionName("menu", "dishes", "org_1")
	if count, _ := store.Store().Count(ctx, name, Query{OrganizationID: "org_1"}, Fence{}); count != 1 {
		t.Fatalf("hot count = %d, want 1 (dish_2 only)", count)
	}

	// Cursor advanced: the same tombstone is not re-swept.
	if n, _ := sweeper.SweepOnce(ctx); n != 0 {
		t.Fatalf("idle delete sweep swept %d, want 0", n)
	}
}
