package placement

import (
	"context"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func TestMirrorFrameRoundTripsEveryField(t *testing.T) {
	lanes := []LaneMirrorUpdate{
		{Lane: 0, Jurisdiction: 7, MaxConcurrency: 4, Inflight: 1, Generation: 3,
			Class: LaneClassHub, UnitClassMask: 0b101, AffinityBloom: 1 << 9,
			EwmaNs: 42_000, TickSeen: 98_000},
		{Lane: 31, Jurisdiction: 0, MaxConcurrency: 8, Inflight: 0, Generation: 12,
			Class: LaneClassEdge, UnitClassMask: 1 << 40, AffinityBloom: 0,
			EwmaNs: ^uint64(0), TickSeen: 99_999},
	}

	frame, err := EncodeLaneMirrorFrame("node-alpha", "eu-frankfurt", LaneClassSeed, lanes)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeLaneMirrorFrame(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.NodeID != "node-alpha" || decoded.RegionID != "eu-frankfurt" || decoded.Class != LaneClassSeed {
		t.Fatalf("identity mismatch: %+v", decoded)
	}
	for i, want := range lanes {
		if decoded.Lanes[i] != want {
			t.Fatalf("lane %d mismatch:\n got %+v\nwant %+v", i, decoded.Lanes[i], want)
		}
	}
}

func TestMirrorFrameRejectsMalformedInput(t *testing.T) {
	if _, err := EncodeLaneMirrorFrame("", "r", LaneClassEdge, []LaneMirrorUpdate{{}}); err == nil {
		t.Fatal("empty node id must fail")
	}
	good, err := EncodeLaneMirrorFrame("n", "r", LaneClassEdge, []LaneMirrorUpdate{{Lane: 1}})
	if err != nil {
		t.Fatalf("baseline encode: %v", err)
	}
	if _, err := DecodeLaneMirrorFrame(good[:len(good)-10]); err == nil {
		t.Fatal("truncated frame must be refused")
	}
	tampered := append([]byte(nil), good...)
	tampered[0] = MirrorWireVersion + 1
	if _, err := DecodeLaneMirrorFrame(tampered); err == nil {
		t.Fatal("unknown version must be refused")
	}
}

func TestPublishAndListenMirrorsOverSharedBus(t *testing.T) {
	bus := rediskit.NewMemoryClient("placement")
	sink := &collectingSink{}
	received := make(chan error, 8)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ListenMirrors(ctx, bus, "", sink, func(_ int, cause error) { received <- cause }); err != nil {
		t.Fatalf("listen: %v", err)
	}

	want := LaneMirrorUpdate{
		Lane: 5, Jurisdiction: 2, MaxConcurrency: 6, Inflight: 2, Generation: 9,
		Class: LaneClassHub, UnitClassMask: 0b11, AffinityBloom: 1 << 20,
		EwmaNs: 77_000, TickSeen: 555,
	}
	if err := PublishLaneMirrors(ctx, bus, "", "edge-1", "us-east", LaneClassEdge, []LaneMirrorUpdate{want}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool { return sink.updates() > 0 })
	if got := sink.last; got != want {
		t.Fatalf("applied %+v want %+v", got, want)
	}
	select {
	case err := <-received:
		t.Fatalf("unexpected listener error: %v", err)
	default:
	}
}

func TestListenerSurvivesForeignFrames(t *testing.T) {
	bus := rediskit.NewMemoryClient("placement")
	sink := &collectingSink{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ListenMirrors(ctx, bus, "", sink, nil); err != nil {
		t.Fatalf("listen: %v", err)
	}

	// Foreign traffic on the shared channel is expected, not fatal.
	if err := bus.Publish(ctx, DefaultMirrorChannel, []byte("not-a-mirror-frame")); err != nil {
		t.Fatalf("foreign publish: %v", err)
	}

	want := LaneMirrorUpdate{Lane: 2, EwmaNs: 900, TickSeen: 7}
	if err := PublishLaneMirrors(ctx, bus, "", "n", "r", LaneClassEdge, []LaneMirrorUpdate{want}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return sink.updates() == 1 })
	if sink.last != want {
		t.Fatalf("post-noise apply = %+v want %+v", sink.last, want)
	}
}

type collectingSink struct {
	last   LaneMirrorUpdate
	called int
}

func (c *collectingSink) ApplyMirrorUpdate(update LaneMirrorUpdate) error {
	c.last = update
	c.called++
	return nil
}

func (c *collectingSink) updates() int { return c.called }

func waitFor(t *testing.T, timeout time.Duration, probe func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if probe() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached before deadline")
}

func TestRemoteTicketValidation(t *testing.T) {
	chunk := hermes.ChunkDescriptor{
		Index: 4, RecordCount: 10_000, PayloadOffset: 4096, PayloadLength: 2048,
		Checksum: [32]byte{0xAA},
	}
	valid := TicketFromChunkDescriptor("orders.projection", "orders:org-1", chunk, 0b01, 7, 250_000)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid ticket refused: %v", err)
	}
	if len(valid.ChecksumHex) != 64 || valid.ChecksumHex[:2] != "aa" {
		t.Fatalf("checksum hex = %q", valid.ChecksumHex)
	}

	broken := valid
	broken.ProjectionID = ""
	if err := broken.Validate(); err == nil {
		t.Fatal("missing projection id must fail")
	}
	broken = valid
	broken.PayloadLen = 0
	if err := broken.Validate(); err == nil {
		t.Fatal("empty payload must fail")
	}
	broken = valid
	broken.DeadlineNs = 0
	if err := broken.Validate(); err == nil {
		t.Fatal("zero deadline must fail")
	}
}
