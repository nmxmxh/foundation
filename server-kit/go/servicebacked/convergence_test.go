//go:build servicebacked

package servicebacked

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

// Projection convergence lives in the seam between three things — a
// ProjectedRuntimeStore, the event bus, and a MirrorSweeper — and each has unit
// tests that pass while the lane converges nothing. The failures that matter
// here are only reachable with two independent processes, one real Postgres and
// one real Redis:
//
//   - a single process converges its own writes through its own store and
//     proves nothing about the lane;
//   - an in-memory bus hands a subscriber the same Envelope value the publisher
//     built, so a subscriber that reads a field the wire does not carry works
//     in-process and silently discards every message in production;
//   - the announcement a process must ignore (its own) and the one it must act
//     on (everybody else's) are the same bytes on the same channel, and only a
//     second bus connection tells them apart.
//
// Four properties are asserted, in the order they can break:
//
//	signal     an application write on one process converges on the other
//	           without either waiting for a timer
//	no echo    the converging process does not re-announce what it converged
//	reconcile  a write no application made still converges
//	idle       nothing polls the record store hard when nothing is happening
//
// The sweep interval is deliberately an hour for the signal legs: a budget
// would only say the signal was faster than the timer, while an hour says the
// timer cannot be what converged it.

const (
	convergenceDomain     = "communication"
	convergenceCollection = "presence"
)

// convergenceNode is one process: its own connection pool, its own projected
// store and hot state, its own bus connection, its own sweeper.
type convergenceNode struct {
	name    string
	db      database.RuntimeStore
	store   *hermes.ProjectedRuntimeStore
	sweeper *hermes.MirrorSweeper
	signal  *hermes.ScopeSignal
	bus     *events.RedisBus
	polls   atomic.Int64
	cancel  context.CancelFunc
	stopped chan struct{}
}

func (n *convergenceNode) close() {
	if n.cancel != nil {
		n.cancel()
		<-n.stopped
	}
	_ = n.signal.Close()
	_ = n.bus.Close()
	n.store.Close()
	n.db.Close()
}

// scopeSourceName is the name both halves of the lane derive independently.
func convergenceScope() string {
	return hermes.ScopeSourceName(convergenceDomain, convergenceCollection)
}

func convergenceDeleteScope() string {
	return hermes.ScopeDeleteSourceName(convergenceDomain, convergenceCollection)
}

// applySeatSchema creates the normalized table the sweeper reads and the
// tombstone table that carries its hard deletes, which an updated_at sweep
// cannot see.
func applySeatSchema(tb testing.TB, ctx context.Context, store database.RuntimeStore) {
	tb.Helper()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS service_backed_seats (
			seat_id TEXT NOT NULL,
			organization_id TEXT NOT NULL,
			room_id TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (organization_id, seat_id)
		)`,
		`CREATE TABLE IF NOT EXISTS service_backed_seat_tombstones (
			seat_id TEXT NOT NULL,
			organization_id TEXT NOT NULL,
			room_id TEXT NOT NULL,
			deleted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (organization_id, seat_id)
		)`,
	}
	for _, statement := range statements {
		if err := store.Exec(ctx, statement); err != nil {
			tb.Fatalf("apply seat schema failed: %v", err)
		}
	}
}

func cleanupSeats(tb testing.TB, ctx context.Context, store database.RuntimeStore, orgID string) {
	tb.Helper()
	for _, table := range []string{"service_backed_seats", "service_backed_seat_tombstones"} {
		if err := store.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE organization_id = $1`, table), orgID); err != nil {
			tb.Fatalf("seat cleanup failed: %v", err)
		}
	}
}

// seatsChangedSince is the app-side ChangedSince: rows of the normalized table
// newer than the cursor, ascending, bounded.
func seatsChangedSince(db database.RuntimeStore, orgID string, polls *atomic.Int64) hermes.ChangedSince {
	return func(ctx context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		polls.Add(1)
		rows, err := db.Query(ctx,
			`SELECT seat_id, room_id, updated_at FROM service_backed_seats
			 WHERE organization_id = $1 AND updated_at > $2
			 ORDER BY updated_at ASC LIMIT 256`, orgID, cursor)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var seatID, roomID string
			var updatedAt time.Time
			if err := rows.Scan(&seatID, &roomID, &updatedAt); err != nil {
				return err
			}
			rec := database.DomainRecord{
				Domain:         convergenceDomain,
				Collection:     convergenceCollection,
				OrganizationID: orgID,
				RecordID:       seatID,
				Data:           serviceRecordData(map[string]any{"room_id": roomID}),
			}
			if err := visit(rec, updatedAt); err != nil {
				return err
			}
		}
		return rows.Err()
	}
}

// seatsDeletedSince carries each deletion's audience field (room_id) so the
// tombstone can be addressed to the subscribers the record itself reached.
func seatsDeletedSince(db database.RuntimeStore, orgID string, polls *atomic.Int64) hermes.DeletedRecordsSince {
	return func(ctx context.Context, cursor time.Time, visit func(database.DomainRecord, time.Time) error) error {
		polls.Add(1)
		rows, err := db.Query(ctx,
			`SELECT seat_id, room_id, deleted_at FROM service_backed_seat_tombstones
			 WHERE organization_id = $1 AND deleted_at > $2
			 ORDER BY deleted_at ASC LIMIT 256`, orgID, cursor)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var seatID, roomID string
			var deletedAt time.Time
			if err := rows.Scan(&seatID, &roomID, &deletedAt); err != nil {
				return err
			}
			rec := database.DomainRecord{
				Domain:         convergenceDomain,
				Collection:     convergenceCollection,
				OrganizationID: orgID,
				RecordID:       seatID,
				Data:           serviceRecordData(map[string]any{"room_id": roomID}),
			}
			if err := visit(rec, deletedAt); err != nil {
				return err
			}
		}
		return rows.Err()
	}
}

// startConvergenceNode builds one process and starts its sweeper.
func startConvergenceNode(
	tb testing.TB,
	ctx context.Context,
	env serviceEnv,
	channel, name, orgID string,
	interval time.Duration,
) *convergenceNode {
	tb.Helper()

	client, err := rediskit.ConnectWithOptions(rediskit.Options{
		URL:          env.redisURL,
		Prefix:       env.prefix,
		Driver:       rediskit.DriverRedis,
		PoolSize:     8,
		MinIdle:      2,
		MaxRetries:   1,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	if err != nil {
		tb.Fatalf("%s: connect redis failed: %v", name, err)
	}
	node := &convergenceNode{name: name, stopped: make(chan struct{})}
	node.bus = events.NewRedisBus(client, channel, 64, nil)
	node.db = openPostgres(tb, env, serviceBackedPoolOptions(4))

	node.signal, err = hermes.NewScopeSignal(hermes.ScopeSignalOptions{
		Bus:     node.bus,
		OnError: func(err error) { tb.Logf("%s: scope signal: %v", name, err) },
	})
	if err != nil {
		tb.Fatalf("%s: NewScopeSignal() error = %v", name, err)
	}

	node.store, err = hermes.WrapRuntimeStore(node.db, hermes.RuntimeStoreOptions{
		MaxRecordsPerScope: 256,
		MaxBytesPerScope:   1 << 20,
		OnCommandWrite:     node.signal.OnCommandWrite,
	})
	if err != nil {
		tb.Fatalf("%s: WrapRuntimeStore() error = %v", name, err)
	}

	node.sweeper, err = hermes.NewMirrorSweeper(node.store, hermes.MirrorSweepOptions{
		Interval:  interval,
		BatchSize: 64,
	})
	if err != nil {
		tb.Fatalf("%s: NewMirrorSweeper() error = %v", name, err)
	}
	if err := node.sweeper.AddScopeSource(convergenceDomain, convergenceCollection,
		seatsChangedSince(node.db, orgID, &node.polls)); err != nil {
		tb.Fatalf("%s: AddScopeSource() error = %v", name, err)
	}
	if err := node.sweeper.AddScopeDeleteRecordSource(convergenceDomain, convergenceCollection,
		seatsDeletedSince(node.db, orgID, &node.polls)); err != nil {
		tb.Fatalf("%s: AddScopeDeleteRecordSource() error = %v", name, err)
	}
	if err := node.signal.Converge(node.sweeper); err != nil {
		tb.Fatalf("%s: Converge() error = %v", name, err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	node.cancel = cancel
	go func() {
		defer close(node.stopped)
		_ = node.sweeper.Run(runCtx)
	}()
	return node
}

// writeSeat is the application write: the normalized row, then the projection
// through the store. The store write is what announces the scope.
func writeSeat(tb testing.TB, ctx context.Context, node *convergenceNode, orgID, seatID, roomID string) {
	tb.Helper()
	if err := node.db.Exec(ctx,
		`INSERT INTO service_backed_seats (seat_id, organization_id, room_id, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (organization_id, seat_id) DO UPDATE SET room_id = EXCLUDED.room_id, updated_at = NOW()`,
		seatID, orgID, roomID); err != nil {
		tb.Fatalf("insert seat failed: %v", err)
	}
	if _, err := node.store.UpsertRecord(ctx, database.DomainRecord{
		Domain:         convergenceDomain,
		Collection:     convergenceCollection,
		OrganizationID: orgID,
		RecordID:       seatID,
		Data:           serviceRecordData(map[string]any{"room_id": roomID}),
	}); err != nil {
		tb.Fatalf("project seat failed: %v", err)
	}
}

// deleteSeat is the application delete: the row goes, a tombstone carrying the
// audience field takes its place, and the projection delete announces the
// tombstone scope.
func deleteSeat(tb testing.TB, ctx context.Context, node *convergenceNode, orgID, seatID, roomID string) {
	tb.Helper()
	if err := node.db.Exec(ctx,
		`DELETE FROM service_backed_seats WHERE organization_id = $1 AND seat_id = $2`, orgID, seatID); err != nil {
		tb.Fatalf("delete seat failed: %v", err)
	}
	if err := node.db.Exec(ctx,
		`INSERT INTO service_backed_seat_tombstones (seat_id, organization_id, room_id, deleted_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (organization_id, seat_id) DO UPDATE SET deleted_at = NOW()`,
		seatID, orgID, roomID); err != nil {
		tb.Fatalf("insert tombstone failed: %v", err)
	}
	if err := node.store.DeleteRecordWithFields(ctx, database.DomainRecord{
		Domain:         convergenceDomain,
		Collection:     convergenceCollection,
		OrganizationID: orgID,
		RecordID:       seatID,
		Data:           serviceRecordData(map[string]any{"room_id": roomID}),
	}); err != nil {
		tb.Fatalf("project seat delete failed: %v", err)
	}
}

// projectedRecords reads how many records a node holds in its own hot
// projection for the scope.
//
// This deliberately does not go through GetRecord. The durable mirror behind
// every projected store is one shared table, so the writer's row is in it the
// moment the writer commits — and GetRecord falls back to that table on a hot
// miss, finds the row, and projects it. A test polling GetRecord on the reader
// would therefore report "converged" for a lane that converged nothing, and
// would create the very state it was checking for while doing it. Hot state is
// the only thing convergence actually moves, so hot state is what to read.
func projectedRecords(node *convergenceNode, orgID string) (records, tombstones int) {
	name := node.store.ProjectionName(convergenceDomain, convergenceCollection, orgID)
	for _, stats := range node.store.HermesRuntimeStats().Projections {
		if stats.Projection == name {
			return stats.Records, stats.Tombstones
		}
	}
	return 0, 0
}

// awaitProjected waits for a node's hot projection to hold want records. A
// budget is the only way to fail a lane that never converges, so every wait has
// one.
func awaitProjected(tb testing.TB, node *convergenceNode, orgID string, want int, budget time.Duration) time.Duration {
	tb.Helper()
	start := time.Now()
	deadline := start.Add(budget)
	for time.Now().Before(deadline) {
		if records, _ := projectedRecords(node, orgID); records == want {
			return time.Since(start)
		}
		time.Sleep(2 * time.Millisecond)
	}
	records, tombstones := projectedRecords(node, orgID)
	tb.Fatalf("%s: projection held %d records (%d tombstones) after %s, want %d",
		node.name, records, tombstones, budget, want)
	return 0
}

// TestServiceBackedProjectionConvergence drives the whole lane across two
// processes against live Postgres and live Redis.
func TestServiceBackedProjectionConvergence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	env := requireServiceEnv(t)
	orgID := uniqueName(env.prefix, "converge-org")
	channel := uniqueName(env.prefix, "converge-bus")

	schemaDB := openPostgres(t, env, serviceBackedPoolOptions(2))
	applyStateSchema(t, ctx, schemaDB)
	applySeatSchema(t, ctx, schemaDB)
	cleanupSeats(t, ctx, schemaDB, orgID)
	cleanupOrganization(t, ctx, schemaDB, orgID)
	defer func() {
		cleanupSeats(t, context.Background(), schemaDB, orgID)
		cleanupOrganization(t, context.Background(), schemaDB, orgID)
		schemaDB.Close()
	}()

	// An hour of reconcile interval: whatever converges below, the timer is not
	// what did it.
	const neverOnTheTimer = time.Hour
	writer := startConvergenceNode(t, ctx, env, channel, "writer", orgID, neverOnTheTimer)
	defer writer.close()
	reader := startConvergenceNode(t, ctx, env, channel, "reader", orgID, neverOnTheTimer)
	defer reader.close()

	// Both processes have completed their opening full sync before anything is
	// written, so a later convergence cannot be that pass arriving late.
	awaitConvergenceCondition(t, "both nodes to complete their first pass", 10*time.Second, func() bool {
		return writer.polls.Load() >= 2 && reader.polls.Load() >= 2
	})
	readerPollsBefore := reader.polls.Load()
	readerSignalledBefore := reader.sweeper.Stats().Signalled
	firstPassAt := time.Now()

	t.Run("signal", func(t *testing.T) {
		if records, _ := projectedRecords(reader, orgID); records != 0 {
			t.Fatalf("the reader started with %d records projected, want 0", records)
		}
		writeSeat(t, ctx, writer, orgID, "seat_1", "room_a")

		took := awaitProjected(t, reader, orgID, 1, 5*time.Second)
		t.Logf("converged on the second process after %s (reconcile interval %s)", took.Round(time.Millisecond), neverOnTheTimer)

		if got := reader.sweeper.Stats().Signalled; got == 0 {
			t.Fatal("the reader converged with no signalled sweeps: the lane did not carry the write")
		}
		if got := reader.signal.Stats().Received; got == 0 {
			t.Fatal("the reader converged without receiving an announcement")
		}
		if got := writer.signal.Stats().Announced; got != 1 {
			t.Fatalf("the writer announced %d times for one write, want 1", got)
		}
	})

	t.Run("no echo", func(t *testing.T) {
		// The reader has now converged a write it did not make. If convergence
		// announced, every replica would rebroadcast every write it received
		// and fleet traffic would grow with the square of the fleet.
		time.Sleep(250 * time.Millisecond) // let a stray announcement reach the bus
		if got := reader.signal.Stats().Announced; got != 0 {
			t.Fatalf("the converging process announced %d times, want 0 — the lane echoes", got)
		}
		if got := writer.signal.Stats().Received; got != 0 {
			t.Fatalf("the writer received %d announcements of its own write, want 0", got)
		}
		if got := writer.signal.Stats().Echoes; got == 0 {
			t.Fatal("the writer never saw its own announcement come back: the echo guard was not exercised")
		}
	})

	t.Run("delete converges addressed", func(t *testing.T) {
		writeSeat(t, ctx, writer, orgID, "seat_2", "room_b")
		awaitProjected(t, reader, orgID, 2, 5*time.Second)

		// A hard delete leaves no row for an updated_at sweep to find, so it
		// converges through the tombstone source under its own name. If the
		// announcement named the changed-rows source instead, this leg is the
		// one that catches it — the write half would still look perfect.
		deleteSeat(t, ctx, writer, orgID, "seat_2", "room_b")
		took := awaitProjected(t, reader, orgID, 1, 5*time.Second)
		if _, tombstones := projectedRecords(reader, orgID); tombstones == 0 {
			t.Fatal("the reader dropped the record without a tombstone: the delete was not addressed")
		}
		t.Logf("delete converged on the second process after %s", took.Round(time.Millisecond))
	})

	t.Run("reconcile", func(t *testing.T) {
		// A write no application made — a migration, an admin UPDATE —
		// announces nothing, so only the timer can carry it. This node runs a
		// deliberately fast reconcile against a production default of a minute,
		// so a fixed budget would either pass everything or fail everything.
		const reconcileInterval = 750 * time.Millisecond
		backstop := startConvergenceNode(t, ctx, env, channel, "backstop", orgID, reconcileInterval)
		defer backstop.close()

		// Wait until the backstop has settled before writing anything: its
		// opening full sync sweeps from a zero cursor and would otherwise be
		// what converges the row, leaving this leg asserting nothing about the
		// reconcile timer at all.
		projectedBefore := awaitSteady(t, backstop, orgID, 2*reconcileInterval)
		signalledBefore := backstop.sweeper.Stats().Signalled

		if err := schemaDB.Exec(ctx,
			`INSERT INTO service_backed_seats (seat_id, organization_id, room_id, updated_at)
			 VALUES ($1, $2, $3, NOW())`, "seat_3", orgID, "room_c"); err != nil {
			t.Fatalf("out-of-band insert failed: %v", err)
		}

		took := awaitProjected(t, backstop, orgID, projectedBefore+1, 8*reconcileInterval)
		t.Logf("out-of-band SQL write converged after %s (reconcile interval %s)", took.Round(time.Millisecond), reconcileInterval)

		// The timing is not the proof — a tick can land anywhere in the
		// interval, including immediately. These two are: nothing announced the
		// write, and no sweep of this node was signalled, so the only thing
		// left that could have converged it is the timer.
		if got := backstop.signal.Stats().Received; got != 0 {
			t.Fatalf("the out-of-band write produced %d announcements, want 0 — it was not out of band", got)
		}
		if got := backstop.sweeper.Stats().Signalled; got != signalledBefore {
			t.Fatalf("Signalled went %d -> %d: a signal converged this write, not the timer", signalledBefore, got)
		}
		if got := backstop.sweeper.Stats().Unroutable; got != 0 {
			t.Fatalf("Unroutable = %d, want 0: a signal named a source nobody registered", got)
		}
	})

	t.Run("idle", func(t *testing.T) {
		// The reconcile interval is an hour, so since its first pass the reader
		// has had no reason to poll except the signals the legs above sent it.
		// A signalled sweep reads one source once, so it costs exactly one poll;
		// a timer pass polls every source and counts as no signal. Any poll the
		// signals do not account for is a sweeper polling on its own — the check
		// that fails if somebody tightens the interval to paper over a broken
		// signal lane. The window is the whole run since the first pass,
		// reconcile leg included, so no idle wait of its own is needed.
		polls := reader.polls.Load() - readerPollsBefore
		signalled := reader.sweeper.Stats().Signalled - readerSignalledBefore
		window := time.Since(firstPassAt).Round(time.Millisecond)
		if polls != signalled {
			t.Fatalf("the reader polled %d times over %s but was signalled %d times: something is sweeping on its own", polls, window, signalled)
		}
		if signalled == 0 {
			t.Fatal("the reader was never signalled: the accounting above compared nothing")
		}
		t.Logf("reader polls: %d total, %d since the first pass, all signalled, over %s", reader.polls.Load(), polls, window)
	})

	for _, node := range []*convergenceNode{writer, reader} {
		stats := node.signal.Stats()
		if stats.Dropped != 0 || stats.Unroutable != 0 || stats.Failures != 0 {
			t.Errorf("%s: scope signal stats = %+v, want no drops, unroutable scopes or failures", node.name, stats)
		}
		// A fallback is a read that went to the shared durable table. None
		// happened, so nothing above was observed through it.
		if got := node.store.HermesRuntimeStats().Fallbacks; got != 0 {
			t.Errorf("%s: %d reads fell back to the durable mirror; hot state was not what was measured", node.name, got)
		}
	}
}

// awaitSteady waits until a node's projection stops changing for a full window
// and returns what it settled on. A reconciling node polls forever by design,
// so "settled" is the projection holding still across passes, not the node
// going quiet.
func awaitSteady(tb testing.TB, node *convergenceNode, orgID string, quiet time.Duration) int {
	tb.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		before, _ := projectedRecords(node, orgID)
		time.Sleep(quiet)
		if after, _ := projectedRecords(node, orgID); after == before {
			return after
		}
	}
	tb.Fatalf("%s: projection never settled within %s windows", node.name, quiet)
	return 0
}

func awaitConvergenceCondition(tb testing.TB, what string, budget time.Duration, cond func() bool) {
	tb.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	tb.Fatalf("timed out after %s waiting for %s", budget, what)
}
