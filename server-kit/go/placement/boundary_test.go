package placement

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func TestMirrorFrameTruncationWalkNeverPanics(t *testing.T) {
	frame, err := EncodeLaneMirrorFrame("node-9", "region-7", LaneClassSeed, benchLanes())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for cut := 1; cut < len(frame); cut++ {
		decoded, err := DecodeLaneMirrorFrame(frame[:cut])
		if err != nil {
			continue
		}
		if len(decoded.Lanes) > 32 || decoded.NodeID != "node-9" {
			t.Fatalf("cut %d produced implausible decode: %+v", cut, decoded)
		}
	}
}

func TestEncodeLaneMirrorFrameRejectsOversizedIdentity(t *testing.T) {
	long := strings.Repeat("x", 300)
	if _, err := EncodeLaneMirrorFrame(long, "r", LaneClassEdge, []LaneMirrorUpdate{{}}); err == nil ||
		!strings.Contains(err.Error(), "node id") {
		t.Fatalf("long node id err = %v", err)
	}
	if _, err := EncodeLaneMirrorFrame("n", long, LaneClassEdge, []LaneMirrorUpdate{{}}); err == nil ||
		!strings.Contains(err.Error(), "region id") {
		t.Fatalf("long region id err = %v", err)
	}
}

var errTransport = errors.New("transport down")

type failingPublishClient struct {
	rediskit.Client
}

func (f *failingPublishClient) Publish(context.Context, string, []byte) error {
	return errTransport
}

func TestPublishLaneMirrorsWrapsTransportErrors(t *testing.T) {
	err := PublishLaneMirrors(context.Background(), &failingPublishClient{rediskit.NewMemoryClient("p")},
		"", "n", "r", LaneClassEdge, []LaneMirrorUpdate{{Lane: 1}})
	if !errors.Is(err, errTransport) {
		t.Fatalf("err = %v want transport wrap", err)
	}
}

var errSubscribe = errors.New("subscribe refused")

type failingSubscribeClient struct {
	rediskit.Client
}

func (f *failingSubscribeClient) Subscribe(context.Context, string) (<-chan []byte, func(), error) {
	return nil, nil, errSubscribe
}

func TestListenMirrorsWrapsSubscribeErrors(t *testing.T) {
	stop, err := ListenMirrors(context.Background(), &failingSubscribeClient{rediskit.NewMemoryClient("p")},
		"", &collectingSink{}, nil)
	if stop != nil {
		t.Fatal("a failed subscribe must not hand back a stop function")
	}
	if !errors.Is(err, errSubscribe) {
		t.Fatalf("err = %v want subscribe wrap", err)
	}
}

// TestListenMirrorsLifecycleBranches covers the remaining listener arms:
// nil error callbacks on both decode and apply paths, ctx cancellation, and
// the subscription-closed exit.
func TestListenMirrorsLifecycleBranches(t *testing.T) {
	bus := rediskit.NewMemoryClient("placement")
	sink := &collectingSink{}
	ctx, cancel := context.WithCancel(context.Background())
	stop, err := ListenMirrors(ctx, bus, "", sink, nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Ends the listener and waits for it, so nothing applies into the sink
	// after the test stops looking at it.
	defer stop()

	// Invalid frame with a NIL onError must be skipped silently.
	if err := bus.Publish(ctx, "", []byte("garbage")); err != nil {
		t.Fatalf("publish garbage: %v", err)
	}
	want := LaneMirrorUpdate{Lane: 7, EwmaNs: 2_000, TickSeen: 3}
	if err := PublishLaneMirrors(ctx, bus, "", "n", "r", LaneClassEdge, []LaneMirrorUpdate{want}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return sink.updates() == 1 })

	cancel()
	_ = bus.Close() // closes subscriber channels: exercises the !ok exit
	time.Sleep(5 * time.Millisecond)
}

// TestRemoteComputeHandlerNilSchemaAndEmptyResult covers the remaining
// handler arms: nil-ish schema passthrough and an executor returning empty
// bytes must still produce a well-formed success frame.
func TestRemoteComputeHandlerNilSchemaAndEmptyResult(t *testing.T) {
	ticket := sampleTicket()
	request, err := EncodeRemoteComputeRequest(ticket, []byte{1})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	handler := RemoteComputeHandler(TicketExecutorFunc(
		func(_ context.Context, _ RemoteComputeTicket, payload []byte) ([]byte, error) {
			return nil, nil
		},
	))
	response, err := handler(context.Background(), frameOf(RemoteComputeRequestedEventType, request))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if response.EventType != RemoteComputeSuccessEventType || len(response.Payload) != 0 {
		t.Fatalf("empty-result frame = %+v", response)
	}
}

// controlledChannelClient lets a test drive the listener's message loop
// deterministically: inject frames, then close the channel to force the
// subscription-closed exit while ctx stays live.
type controlledChannelClient struct {
	rediskit.Client
	frames chan []byte
}

func (c *controlledChannelClient) Subscribe(context.Context, string) (<-chan []byte, func(), error) {
	return c.frames, func() {}, nil
}

func TestListenMirrorsDecodeErrorCallbackAndClosedExit(t *testing.T) {
	base := rediskit.NewMemoryClient("placement")
	frames := make(chan []byte, 4)
	client := &controlledChannelClient{Client: base, frames: frames}
	sink := &collectingSink{}

	errs := make(chan error, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop, err := ListenMirrors(ctx, client, "", sink, func(_ int, cause error) { errs <- cause })
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Ends the listener and waits for it, so nothing applies into the sink
	// after the test stops looking at it.
	defer stop()

	// Decode failure surfaces through the non-nil callback...
	frames <- []byte("junk")
	select {
	case <-errs:
	case <-time.After(2 * time.Second):
		t.Fatal("decode error never surfaced")
	}

	// ...and closing the stream ends the listener through the !ok arm.
	close(frames)
	time.Sleep(5 * time.Millisecond)

	// ctx cancellation while the stream is already closed must exit cleanly
	// too (double-exit race arm).
	cancel()
	time.Sleep(5 * time.Millisecond)
}

func TestListenMirrorsApplyErrorWithNilCallbackIsSilent(t *testing.T) {
	base := rediskit.NewMemoryClient("placement")
	frames := make(chan []byte, 4)
	client := &controlledChannelClient{Client: base, frames: frames}
	sink := &collectingSink{}
	ctx := t.Context()
	stop, err := ListenMirrors(ctx, client, "", sink, nil)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Ends the listener and waits for it, so nothing applies into the sink
	// after the test stops looking at it.
	defer stop()
	// Sub-floor claim with a NIL onError: skipped silently, no panic.
	frames <- []byte{MirrorWireVersion, 0, 1, 'n', 1, 'r', 0, 1, 0, 0, 0, 0, 0, 0, 0, byte(LaneClassEdge)}
	frames <- func() []byte {
		frame, _ := EncodeLaneMirrorFrame("liar", "r", LaneClassEdge,
			[]LaneMirrorUpdate{{Lane: 2, EwmaNs: MinPlausibleEwmaNs - 3}})
		return frame
	}()
	time.Sleep(20 * time.Millisecond)
	if sink.updates() != 0 {
		t.Fatal("rejected update reached sink")
	}
}
