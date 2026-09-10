package hermes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

// ScopeSignal is the change-notification lane for projections: the process that
// wrote a scope says so on the bus the deployment already runs, and every other
// process converges that scope without waiting for a timer.
//
// The alternative — a trigger on the record store calling pg_notify — costs 78%
// of write throughput at c=64 and does not scale with concurrency at all,
// because committing a transaction that notified takes a global exclusive lock.
// See docs/database_practices.md, "Change Notification On The Record Store".
// The application already knows what it wrote; deriving it from the database is
// paying to rediscover it in the one place contention is most expensive.
//
// Three properties make the lane safe to run in a fleet, and each is a mistake
// that is easy to make by hand and invisible once made:
//
//   - The announcement is a hint, never data. It names a scope and carries no
//     records, so losing one costs time and not correctness — the cursor read
//     behind it reads everything since the cursor whenever it next runs.
//   - Convergence writes are never announced. A replica that rebroadcast what
//     it just converged would make the fleet's traffic grow with the square of
//     its size. ProjectedRuntimeStore enforces this by routing sweeper writes
//     through unexported variants rather than by a runtime flag.
//   - Publishing and converging both stay off the goroutine that triggered
//     them. Announcing hands a scope name to a buffered channel and returns, so
//     no commit waits on Redis; converging flags a source and returns, so no
//     bus dispatch waits on Postgres.
//
// Wiring, in the order the dependencies allow:
//
//	signal, err := hermes.NewScopeSignal(hermes.ScopeSignalOptions{Bus: bus})
//	store, err := hermes.WrapRuntimeStore(db, hermes.RuntimeStoreOptions{
//	    OnCommandWrite: signal.OnCommandWrite,
//	})
//	sweeper, err := hermes.NewMirrorSweeper(store, hermes.MirrorSweepOptions{Interval: time.Minute})
//	sweeper.AddScopeSource("menu", "dishes", dishesChangedSince)
//	err = signal.Converge(sweeper)   // subscribe, after the sources exist
//	go sweeper.Run(ctx)
type ScopeSignal struct {
	bus        events.Bus
	nodeID     string
	sourceName func(database.DomainRecord, Operation) string
	onError    func(error)

	// closeMu makes "still open" and "send" one step. Checking a flag and
	// then sending is a send on a closed channel — a panic — whenever a write
	// commits while the process is shutting down. Readers do not contend with
	// each other, so the write path pays a shared lock and nothing else.
	queue   chan string
	closeMu sync.RWMutex
	closed  atomic.Bool
	done    chan struct{}
	once    sync.Once

	// pending is the set of scopes already queued and not yet published. A
	// scope announced twice before either announcement goes out is one
	// announcement: the hint carries no data, so a second copy of it says
	// exactly what the first one already said.
	pendingMu sync.Mutex
	pending   map[string]struct{}

	sweeperMu sync.RWMutex
	sweeper   *MirrorSweeper

	announced  atomic.Int64
	coalesced  atomic.Int64
	dropped    atomic.Int64
	received   atomic.Int64
	echoes     atomic.Int64
	unroutable atomic.Int64
	failures   atomic.Int64
}

const (
	// ScopeChangedEventType is the event type a scope announcement is published
	// under. It is deliberately one type for every scope: subscribers filter on
	// the scope in the payload, and a deployment adding a projection does not
	// have to add a subscription.
	ScopeChangedEventType = "hermes:scope_changed:v1:success"

	// scopePayloadScope names the source the announcement is about, and
	// scopePayloadNode the process that sent it.
	scopePayloadScope = "scope"
	scopePayloadNode  = "node_id"

	defaultScopeSignalQueue = 256
)

// ScopeSignalOptions configures the lane.
type ScopeSignalOptions struct {
	// Bus is the event bus announcements travel on. Required.
	Bus events.Bus

	// SourceName maps a committed record and the operation that wrote it to the
	// MirrorSweeper source that converges its scope. Defaults to
	// ScopeSourceName / ScopeDeleteSourceName, which is what AddScopeSource and
	// its delete counterparts register under — so a project that registers
	// through those has no naming contract to keep in agreement. Supply this
	// only when sources are registered under names of your own.
	SourceName func(rec database.DomainRecord, op Operation) string

	// NodeID identifies this process, so its own announcements can be told from
	// everybody else's. Defaults to 16 random bytes.
	//
	// Do not derive it from the clock. Two processes started together on one
	// host can read the same value, and two nodes sharing an id each mistake
	// the other's announcements for an echo of their own and skip them — which
	// looks exactly like convergence quietly not happening.
	NodeID string

	// QueueSize bounds the announcements waiting to be published (default 256).
	// A full queue drops rather than blocks: the writer must not wait on Redis,
	// and a dropped hint costs staleness until the reconcile pass, not data.
	QueueSize int

	// OnError observes publish and convergence failures. Optional; the counters
	// in Stats record them either way.
	OnError func(error)
}

// NewScopeSignal starts the announcement lane. Bind it to a sweeper with
// Converge once the sources exist.
func NewScopeSignal(opts ScopeSignalOptions) (*ScopeSignal, error) {
	if opts.Bus == nil {
		return nil, errors.New("hermes scope signal requires an event bus")
	}
	nodeID := strings.TrimSpace(opts.NodeID)
	if nodeID == "" {
		generated, err := randomToken()
		if err != nil {
			return nil, fmt.Errorf("hermes scope signal node id: %w", err)
		}
		nodeID = generated
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultScopeSignalQueue
	}
	sourceName := opts.SourceName
	if sourceName == nil {
		sourceName = DefaultScopeSourceName
	}
	s := &ScopeSignal{
		bus:        opts.Bus,
		nodeID:     nodeID,
		sourceName: sourceName,
		onError:    opts.OnError,
		queue:      make(chan string, opts.QueueSize),
		pending:    make(map[string]struct{}, 8),
		done:       make(chan struct{}),
	}
	go s.publishLoop()
	return s, nil
}

// NodeID is this process's identity on the lane.
func (s *ScopeSignal) NodeID() string { return s.nodeID }

// Converge subscribes this process to other processes' announcements and
// converges the named source through sweeper. Call it after the sweeper's
// sources are registered: from that point announcements naming a source nobody
// registered are counted as unroutable rather than published, which is how a
// naming mistake surfaces instead of looking like convergence.
func (s *ScopeSignal) Converge(sweeper *MirrorSweeper) error {
	if sweeper == nil {
		return errors.New("hermes scope signal requires a sweeper to converge")
	}
	s.sweeperMu.Lock()
	if s.sweeper != nil {
		s.sweeperMu.Unlock()
		return errors.New("hermes scope signal is already converging a sweeper")
	}
	s.sweeper = sweeper
	s.sweeperMu.Unlock()

	s.bus.Subscribe(ScopeChangedEventType, s.receive)
	return nil
}

// OnCommandWrite is the RuntimeStoreOptions hook. It runs on the writer's
// goroutine immediately after a command write commits, so it does no I/O: it
// resolves the scopes the batch touched and hands them to the publisher.
func (s *ScopeSignal) OnCommandWrite(_ context.Context, records []database.DomainRecord, op Operation) {
	if s == nil || s.closed.Load() || len(records) == 0 {
		return
	}
	// enqueue folds duplicates too, but it takes a lock to do it. A batch is
	// usually one scope and never many, so a linear scan of the names already
	// handled keeps a 256-record batch to one lock acquisition instead of 256.
	var seen [8]string
	names := seen[:0]
	for _, rec := range records {
		name := strings.TrimSpace(s.sourceName(rec, op))
		if name == "" || containsName(names, name) {
			continue
		}
		names = append(names, name)
		s.enqueue(name)
	}
}

// enqueue offers one scope to the publisher, without blocking.
//
// A scope already queued is folded into the announcement waiting for it rather
// than queued again. That is what keeps bus traffic off the write rate: a
// thousand writes to one scope while one announcement is in flight cost one
// more announcement, not a thousand, on a channel every replica in the fleet is
// listening to.
//
// A full queue drops rather than blocks — the writer must not wait on Redis —
// and with duplicates folded out, the queue only fills when more distinct
// scopes are moving than it has room for. A dropped hint costs staleness until
// the reconcile pass, never data.
func (s *ScopeSignal) enqueue(name string) {
	if !s.routable(name) {
		s.unroutable.Add(1)
		return
	}
	s.closeMu.RLock()
	defer s.closeMu.RUnlock()
	if s.closed.Load() {
		return
	}
	if !s.markPending(name) {
		s.coalesced.Add(1)
		return
	}
	select {
	case s.queue <- name:
	default:
		s.clearPending(name)
		s.dropped.Add(1)
	}
}

// markPending claims the scope, reporting false if an announcement for it is
// already waiting.
func (s *ScopeSignal) markPending(name string) bool {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if _, queued := s.pending[name]; queued {
		return false
	}
	s.pending[name] = struct{}{}
	return true
}

func (s *ScopeSignal) clearPending(name string) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	delete(s.pending, name)
}

// routable reports whether a bound sweeper has a source under this name. With
// no sweeper bound yet — announcements can start the moment the store is live,
// which is before Converge — everything is routable, because the process has
// nothing to check against.
func (s *ScopeSignal) routable(name string) bool {
	s.sweeperMu.RLock()
	sweeper := s.sweeper
	s.sweeperMu.RUnlock()
	if sweeper == nil {
		return true
	}
	return sweeper.hasSource(name)
}

// publishLoop publishes queued scopes on its own context. It never uses the
// writer's context: a request that ended is not a reason to stop telling the
// fleet what it wrote.
func (s *ScopeSignal) publishLoop() {
	defer close(s.done)
	ctx := context.Background()
	for name := range s.queue {
		// Release the scope before publishing, never after: a write that lands
		// while this announcement is in flight is about a change this
		// announcement may not cover, so it has to be able to queue its own.
		s.clearPending(name)
		s.publish(ctx, name)
	}
}

func (s *ScopeSignal) publish(ctx context.Context, scope string) {
	correlationID, err := randomToken()
	if err != nil {
		s.fail(fmt.Errorf("hermes scope signal correlation id: %w", err))
		return
	}
	envelope := events.Envelope{
		EventType: ScopeChangedEventType,
		Payload: extension.Object{
			scopePayloadScope: extension.String(scope),
			scopePayloadNode:  extension.String(s.nodeID),
		},
		PayloadEncoding: events.PayloadEncodingJSON,
		Metadata:        extension.Object{"correlation_id": extension.String(correlationID)},
		CorrelationID:   correlationID,
		SchemaVersion:   events.EnvelopeSchemaVersion,
		Timestamp:       time.Now().UTC(),
	}
	if err := s.bus.Publish(ctx, envelope); err != nil {
		s.fail(fmt.Errorf("hermes scope signal publish %q: %w", scope, err))
		return
	}
	s.announced.Add(1)
}

// receive handles one announcement. It flags the source and returns: a bus
// dispatches every subscription from one goroutine, so sweeping here would put
// a database round trip in front of every other event the process was about to
// handle.
func (s *ScopeSignal) receive(_ context.Context, envelope events.Envelope) {
	if s.closed.Load() {
		return
	}
	// A bus materializes an envelope's metadata on receipt but leaves the
	// payload as bytes, because decoding one for every subscriber of every
	// event is work most of them do not want. Reading through the nil payload
	// yields empty strings rather than an error, so skipping this is a
	// subscriber that silently discards every message it is sent.
	if err := envelope.MaterializePayload(); err != nil {
		s.fail(fmt.Errorf("hermes scope signal payload: %w", err))
		return
	}
	scope, _ := envelope.Payload.GetString(scopePayloadScope)
	node, _ := envelope.Payload.GetString(scopePayloadNode)
	if scope == "" {
		s.fail(errors.New("hermes scope signal received an announcement with no scope"))
		return
	}
	if node == s.nodeID {
		// Our own announcement, delivered locally by the bus on the way out.
		// This process already applied the write that caused it.
		s.echoes.Add(1)
		return
	}
	s.received.Add(1)

	s.sweeperMu.RLock()
	sweeper := s.sweeper
	s.sweeperMu.RUnlock()
	if sweeper == nil {
		return
	}
	if err := sweeper.Notify(scope); err != nil {
		s.unroutable.Add(1)
		s.fail(err)
	}
}

func (s *ScopeSignal) fail(err error) {
	s.failures.Add(1)
	if s.onError != nil {
		s.onError(err)
	}
}

// Close stops the publisher. Announcements already queued are published first;
// the subscription stays registered (buses here do not unsubscribe) but stops
// converging.
func (s *ScopeSignal) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.closeMu.Lock()
		s.closed.Store(true)
		close(s.queue)
		s.closeMu.Unlock()
	})
	<-s.done
	return nil
}

// ScopeSignalStats exposes the lane's counters. They are the cheapest way to
// tell a working lane from a lane that looks like one: a healthy deployment has
// Announced and Received both rising, Unroutable flat at zero, and Dropped
// flat.
type ScopeSignalStats struct {
	// Announced counts announcements published to the bus.
	Announced int64
	// Coalesced counts writes whose announcement was folded into one already
	// queued for the same drain. Against Announced it is what the queue saved.
	Coalesced int64
	// Dropped counts announcements discarded because the queue was full. A
	// dropped hint costs staleness until the reconcile pass, never data.
	Dropped int64
	// Received counts announcements from other processes.
	Received int64
	// Echoes counts this process's own announcements, delivered back to it by
	// the bus and skipped.
	Echoes int64
	// Unroutable counts announcements naming a source nobody registered. Any
	// value but zero is a wiring bug: that scope converges on the timer alone.
	Unroutable int64
	// Failures counts publish and decode errors.
	Failures int64
}

// Stats returns the lane's counters.
func (s *ScopeSignal) Stats() ScopeSignalStats {
	return ScopeSignalStats{
		Announced:  s.announced.Load(),
		Coalesced:  s.coalesced.Load(),
		Dropped:    s.dropped.Load(),
		Received:   s.received.Load(),
		Echoes:     s.echoes.Load(),
		Unroutable: s.unroutable.Load(),
		Failures:   s.failures.Load(),
	}
}

// ScopeSourceName is the sweeper source name for a scope's changed rows.
func ScopeSourceName(domain, collection string) string {
	return strings.TrimSpace(domain) + ":" + strings.TrimSpace(collection)
}

// ScopeDeleteSourceName is the sweeper source name for a scope's tombstones.
func ScopeDeleteSourceName(domain, collection string) string {
	return ScopeSourceName(domain, collection) + ":deletes"
}

// DefaultScopeSourceName routes a committed record to the source that converges
// it: upserts to the scope's changed-rows source, deletions to its tombstone
// source. A hard delete leaves no row for an updated_at sweep to find, so the
// two are different sources and an announcement has to name the right one.
func DefaultScopeSourceName(rec database.DomainRecord, op Operation) string {
	if op == OperationDelete {
		return ScopeDeleteSourceName(rec.Domain, rec.Collection)
	}
	return ScopeSourceName(rec.Domain, rec.Collection)
}

// containsName reports whether name is already in names. Linear because the
// slice is a handful of scopes at most.
func containsName(names []string, name string) bool {
	for _, existing := range names {
		if existing == name {
			return true
		}
	}
	return false
}

// randomToken returns 16 random bytes as hex — a valid metadata token, and not
// a clock.
func randomToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
