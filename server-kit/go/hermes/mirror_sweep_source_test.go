package hermes

import (
	"context"
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
