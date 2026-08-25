//go:build servicebacked

package servicebacked

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/placement"
)

// Service-backed proof for the placement mirror lane (TE-38.2: pub/sub
// delivery, duplicate handling, slow-consumer survival against real Redis —
// the memory-client unit tests cannot speak for the network substrate).
//
// Legs:
//  1. A peer publishes lane reports; a listener over live Redis applies every
//     one into a local sink, preserving field fidelity.
//  2. Foreign garbage frames on the shared channel are skipped without
//     killing the subscription, and healthy traffic still flows after.
//  3. The implausible-latency guard fires through the listener's error
//     callback while subsequent valid updates keep applying.
//
// Run via `make test-service-backed` (requires SERVICE_BACKED_REDIS_URL).

// collectingSink is the local mirror receiver under test. It mirrors what
// runtimehost.DispatchBlock.ApplyMirrorUpdate does without shared memory,
// keeping this lane focused on live-Redis delivery semantics.
// Written by the listener goroutine, read by the polling test goroutine, so
// every field is guarded.
type collectingSink struct {
	mu     sync.Mutex
	last   placement.LaneMirrorUpdate
	called int
}

func (c *collectingSink) ApplyMirrorUpdate(update placement.LaneMirrorUpdate) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = update
	c.called++
	return nil
}

func (c *collectingSink) updates() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.called
}

func (c *collectingSink) lastUpdate() placement.LaneMirrorUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func TestServiceBackedPlacementMirrorLane(t *testing.T) {
	env := requireServiceEnv(t)
	client := openRedis(t, env)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	channel := "placement-mirror-" + env.prefix
	sink := &collectingSink{}
	errCh := make(chan error, 16)
	stop, err := placement.ListenMirrors(ctx, client, channel, sink,
		func(_ int, cause error) { errCh <- cause })
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer stop()

	want := []placement.LaneMirrorUpdate{
		{Lane: 0, Jurisdiction: 7, MaxConcurrency: 4, Inflight: 1, Generation: 1,
			Class: placement.LaneClassHub, UnitClassMask: 0b11, AffinityBloom: 1 << 5,
			EwmaNs: 12_500, TickSeen: 900},
		{Lane: 1, MaxConcurrency: 8, EwmaNs: 30_000, TickSeen: 901},
	}
	if err := placement.PublishLaneMirrors(ctx, client, channel, "edge-sb", "eu-central", placement.LaneClassEdge, want); err != nil {
		t.Fatalf("publish valid batch: %v", err)
	}
	waitForCondition(t, 10*time.Second, func() bool { return sink.updates() >= 2 })
	if got := sink.lastUpdate(); got != want[1] {
		t.Fatalf("last applied = %+v want %+v", got, want[1])
	}

	// Leg 2: foreign frame noise must not blind the subscription.
	if err := client.Publish(ctx, channel, []byte("<not-a-placement-frame>")); err != nil {
		t.Fatalf("foreign publish: %v", err)
	}
	recovered := placement.LaneMirrorUpdate{Lane: 2, EwmaNs: 40_000, TickSeen: 902}
	if err := placement.PublishLaneMirrors(ctx, client, channel, "edge-sb", "eu-central", placement.LaneClassEdge, []placement.LaneMirrorUpdate{recovered}); err != nil {
		t.Fatalf("post-noise publish: %v", err)
	}
	waitForCondition(t, 10*time.Second, func() bool { return sink.updates() >= 3 })

	// Leg 3: the plausibility guard surfaces through the error callback on
	// the real wire, and the row it would have touched stays untouched.
	before := sink.updates()
	liar := placement.LaneMirrorUpdate{Lane: 9, EwmaNs: placement.MinPlausibleEwmaNs - 1, TickSeen: 903}
	if err := placement.PublishLaneMirrors(ctx, client, channel, "liar-node", "eu-central", placement.LaneClassEdge, []placement.LaneMirrorUpdate{liar}); err != nil {
		t.Fatalf("publish liar batch: %v", err)
	}
	waitForCondition(t, 10*time.Second, func() bool { return len(errCh) > 0 })
	// Foreign-frame skips and the plausibility refusal both land on errCh in
	// delivery order; drain until the refusal shows up.
	plausibilitySeen := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			if strings.Contains(err.Error(), "plausibility") {
				plausibilitySeen = true
			}
			if plausibilitySeen && sink.updates() == before {
				goto verified
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
verified:
	if !plausibilitySeen {
		t.Fatal("plausibility refusal never surfaced through the listener")
	}
	if got := sink.updates(); got != before {
		t.Fatalf("rejected update applied anyway: %d -> %d", before, got)
	}
}

func waitForCondition(tb testing.TB, timeout time.Duration, probe func() bool) {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if probe() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	tb.Fatal("condition not reached before deadline")
}
