package transfer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
)

// captureBus records published envelopes and can be forced to fail, so we can
// assert bookend emission and rollback behavior (test oracle: emitted events).
type captureBus struct {
	mu        sync.Mutex
	published []events.Envelope
	failNext  error
}

func (b *captureBus) Publish(_ context.Context, env events.Envelope) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failNext != nil {
		err := b.failNext
		b.failNext = nil
		return err
	}
	b.published = append(b.published, env)
	return nil
}

func (b *captureBus) Subscribe(string, events.Subscriber) {}

func (b *captureBus) Recent(int) []events.Envelope { return nil }

func (b *captureBus) types() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.published))
	for i, e := range b.published {
		out[i] = e.EventType
	}
	return out
}

func (b *captureBus) last() events.Envelope {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.published[len(b.published)-1]
}

func newTestManager(t *testing.T, bus events.Bus, max int) *Manager {
	t.Helper()
	m, err := NewManager(Config{
		Domain:    "media",
		Action:    "upload",
		Version:   "v1",
		Bus:       bus,
		MaxActive: max,
		Now:       fixedClock(),
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestNewManager_Validation(t *testing.T) {
	t.Parallel()
	if _, err := NewManager(Config{Action: "upload"}); err == nil {
		t.Error("missing domain must error")
	}
	if _, err := NewManager(Config{Domain: "media"}); err == nil {
		t.Error("missing action must error")
	}
	// Domain must satisfy the event-type grammar (lower_snake).
	if _, err := NewManager(Config{Domain: "Media", Action: "upload"}); err == nil {
		t.Error("invalid domain must fail event-type validation")
	}
	m, err := NewManager(Config{Domain: "media", Action: "upload"})
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if m.maxActive != DefaultMaxActive {
		t.Errorf("default maxActive=%d want %d", m.maxActive, DefaultMaxActive)
	}
}

func TestManager_EventTypeAssembly(t *testing.T) {
	t.Parallel()
	m := newTestManager(t, nil, 0)
	if got := m.eventType("requested"); got != "media:upload:v1:requested" {
		t.Errorf("eventType=%q", got)
	}
	noVer, _ := NewManager(Config{Domain: "media", Action: "upload"})
	if got := noVer.eventType("success"); got != "media:upload:success" {
		t.Errorf("eventType without version=%q", got)
	}
}

func TestManager_BeginEmitsRequestedAndRegisters(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	m := newTestManager(t, bus, 0)

	tr, err := m.Begin(context.Background(), BeginInput{
		TransferID: "tx-1", CorrelationID: "corr-1", BytesTotal: 500,
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if m.Active() != 1 {
		t.Errorf("Active=%d want 1", m.Active())
	}
	if got, ok := m.Get("tx-1"); !ok || got != tr {
		t.Error("Get must return the registered tracker")
	}
	if types := bus.types(); len(types) != 1 || types[0] != "media:upload:v1:requested" {
		t.Errorf("bookend types=%v want [media:upload:v1:requested]", types)
	}
	// Bookend must carry transfer_id in both payload and metadata, plus correlation.
	env := bus.last()
	if env.CorrelationID != "corr-1" {
		t.Errorf("bookend correlation=%q", env.CorrelationID)
	}
	if v, ok := env.Payload["transfer_id"]; !ok {
		t.Error("payload missing transfer_id")
	} else if s, _ := v.StringValue(); s != "tx-1" {
		t.Errorf("payload transfer_id=%q", s)
	}
	if v, ok := env.Metadata[metaTransferID]; !ok {
		t.Error("metadata missing transfer_id")
	} else if s, _ := v.StringValue(); s != "tx-1" {
		t.Errorf("metadata transfer_id=%q", s)
	}
}

func TestManager_BeginValidation(t *testing.T) {
	t.Parallel()
	m := newTestManager(t, &captureBus{}, 0)
	ctx := context.Background()
	if _, err := m.Begin(ctx, BeginInput{CorrelationID: "c"}); !errors.Is(err, ErrEmptyID) {
		t.Errorf("missing id=%v want ErrEmptyID", err)
	}
	if _, err := m.Begin(ctx, BeginInput{TransferID: "t"}); !errors.Is(err, ErrEmptyCorrelation) {
		t.Errorf("missing correlation=%v want ErrEmptyCorrelation", err)
	}
	if _, err := m.Begin(ctx, BeginInput{TransferID: "t", CorrelationID: "c", BytesTotal: -1}); !errors.Is(err, ErrNegativeBytes) {
		t.Errorf("negative total=%v want ErrNegativeBytes", err)
	}
}

func TestManager_BeginDuplicateRejected(t *testing.T) {
	t.Parallel()
	m := newTestManager(t, &captureBus{}, 0)
	ctx := context.Background()
	in := BeginInput{TransferID: "tx", CorrelationID: "c"}
	if _, err := m.Begin(ctx, in); err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	if _, err := m.Begin(ctx, in); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate=%v want ErrDuplicate", err)
	}
}

func TestManager_CapacityCeiling(t *testing.T) {
	t.Parallel()
	m := newTestManager(t, &captureBus{}, 2)
	ctx := context.Background()
	for i := range 2 {
		if _, err := m.Begin(ctx, BeginInput{
			TransferID: fmt.Sprintf("tx-%d", i), CorrelationID: "c",
		}); err != nil {
			t.Fatalf("Begin %d: %v", i, err)
		}
	}
	_, err := m.Begin(ctx, BeginInput{TransferID: "tx-overflow", CorrelationID: "c"})
	if !errors.Is(err, ErrCapacity) {
		t.Errorf("overflow=%v want ErrCapacity", err)
	}
	// A settled transfer frees a slot.
	if err := m.Complete(ctx, "tx-0", "sum"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := m.Begin(ctx, BeginInput{TransferID: "tx-after", CorrelationID: "c"}); err != nil {
		t.Errorf("Begin after free slot: %v", err)
	}
}

func TestManager_BeginRollsBackOnBookendFailure(t *testing.T) {
	t.Parallel()
	bus := &captureBus{failNext: errors.New("bus down")}
	m := newTestManager(t, bus, 0)
	_, err := m.Begin(context.Background(), BeginInput{TransferID: "tx", CorrelationID: "c"})
	if err == nil {
		t.Fatal("Begin must surface bookend failure")
	}
	if m.Active() != 0 {
		t.Errorf("failed Begin leaked a tracker: Active=%d", m.Active())
	}
	if _, ok := m.Get("tx"); ok {
		t.Error("tracker must be rolled back on bookend failure")
	}
}

func TestManager_CompleteWalksToReadyAndEmitsSuccess(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	m := newTestManager(t, bus, 0)
	ctx := context.Background()
	tr, _ := m.Begin(ctx, BeginInput{TransferID: "tx", CorrelationID: "c", BytesTotal: 100})
	mustAdvance(t, tr, 60) // only partway; Complete must still legally reach Ready.

	if err := m.Complete(ctx, "tx", "checksum-xyz"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if m.Active() != 0 {
		t.Errorf("Active=%d want 0 after Complete", m.Active())
	}
	if tr.Snapshot().Phase != PhaseReady {
		t.Errorf("phase=%s want ready", tr.Snapshot().Phase)
	}
	types := bus.types()
	want := []string{"media:upload:v1:requested", "media:upload:v1:success"}
	if fmt.Sprint(types) != fmt.Sprint(want) {
		t.Errorf("bookends=%v want %v", types, want)
	}
	env := bus.last()
	if v, ok := env.Payload["checksum"]; !ok {
		t.Error("success bookend missing checksum")
	} else if s, _ := v.StringValue(); s != "checksum-xyz" {
		t.Errorf("checksum=%q", s)
	}
}

func TestManager_CompleteFromStagedAndProcessing(t *testing.T) {
	t.Parallel()
	// driveTerminal must be idempotent about phases already passed.
	m := newTestManager(t, &captureBus{}, 0)
	ctx := context.Background()
	tr, _ := m.Begin(ctx, BeginInput{TransferID: "tx", CorrelationID: "c", BytesTotal: 10})
	mustAdvance(t, tr, 10)
	mustTransition(t, tr, PhaseStaged)
	mustTransition(t, tr, PhaseProcessing)
	if err := m.Complete(ctx, "tx", ""); err != nil {
		t.Fatalf("Complete from processing: %v", err)
	}
	if tr.Snapshot().Phase != PhaseReady {
		t.Errorf("phase=%s want ready", tr.Snapshot().Phase)
	}
}

func TestManager_FailAndAbortEmitFailed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		run   func(*Manager, context.Context) error
		phase Phase
	}{
		{"fail", func(m *Manager, ctx context.Context) error { return m.Fail(ctx, "tx", "boom") }, PhaseFailed},
		{"abort", func(m *Manager, ctx context.Context) error { return m.Abort(ctx, "tx", "cancelled") }, PhaseAborted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := &captureBus{}
			m := newTestManager(t, bus, 0)
			ctx := context.Background()
			tr, _ := m.Begin(ctx, BeginInput{TransferID: "tx", CorrelationID: "c", BytesTotal: 100})
			mustAdvance(t, tr, 30)
			if err := tc.run(m, ctx); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if tr.Snapshot().Phase != tc.phase {
				t.Errorf("phase=%s want %s", tr.Snapshot().Phase, tc.phase)
			}
			if m.Active() != 0 {
				t.Errorf("Active=%d want 0", m.Active())
			}
			if last := bus.last().EventType; last != "media:upload:v1:failed" {
				t.Errorf("terminal bookend=%q want media:upload:v1:failed", last)
			}
		})
	}
}

func TestManager_SettleUnknownTransfer(t *testing.T) {
	t.Parallel()
	m := newTestManager(t, &captureBus{}, 0)
	if err := m.Complete(context.Background(), "ghost", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Complete unknown=%v want ErrNotFound", err)
	}
}

func TestManager_SettleReapsEvenIfBookendFails(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	m := newTestManager(t, bus, 0)
	ctx := context.Background()
	tr, _ := m.Begin(ctx, BeginInput{TransferID: "tx", CorrelationID: "c", BytesTotal: 10})
	mustAdvance(t, tr, 10)

	bus.failNext = errors.New("bus down")
	err := m.Complete(ctx, "tx", "")
	if err == nil {
		t.Fatal("Complete must surface bookend failure")
	}
	// Tracker still settled and reaped so memory is not pinned.
	if m.Active() != 0 {
		t.Errorf("Active=%d want 0 (reaped despite bookend failure)", m.Active())
	}
	if tr.Snapshot().Phase != PhaseReady {
		t.Errorf("phase=%s want ready", tr.Snapshot().Phase)
	}
}

// TestManager_BookendInteropsWithRealBus is a producer/consumer contract test:
// bookends emitted by the Manager must satisfy the real event bus's validation
// and be observable by a subscriber on the fact lane.
func TestManager_BookendInteropsWithRealBus(t *testing.T) {
	t.Parallel()
	bus := events.NewInMemoryBus(16)
	var mu sync.Mutex
	var seen []string
	bus.Subscribe("media:upload:v1:*", func(_ context.Context, env events.Envelope) {
		mu.Lock()
		seen = append(seen, env.EventType)
		mu.Unlock()
	})

	m, err := NewManager(Config{Domain: "media", Action: "upload", Version: "v1", Bus: bus, Now: fixedClock()})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ctx := context.Background()
	tr, err := m.Begin(ctx, BeginInput{TransferID: "tx", CorrelationID: "corr-1", BytesTotal: 10})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	mustAdvance(t, tr, 10)
	if err := m.Complete(ctx, "tx", "sum"); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"media:upload:v1:requested", "media:upload:v1:success"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("subscriber saw %v want %v", seen, want)
	}
}
