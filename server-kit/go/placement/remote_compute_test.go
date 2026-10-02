package placement

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

func testChunk(index uint32, payloadLen uint32) hermes.ChunkDescriptor {
	return hermes.ChunkDescriptor{
		Index:         index,
		RecordCount:   10_000,
		PayloadOffset: 4096 * index,
		PayloadLength: payloadLen,
		Checksum:      [32]byte{byte(index), 0xAA},
	}
}

func sampleTicket() RemoteComputeTicket {
	ticket := TicketFromChunkDescriptor(
		"orders.projection", "orders:org-1",
		testChunk(4, 2048),
		0b01, 7, 250_000,
	)
	return ticket
}

func TestRemoteComputeRequestRoundTrips(t *testing.T) {
	ticket := sampleTicket()
	payload := []byte("chunk-bytes-here")

	frame, err := EncodeRemoteComputeRequest(ticket, payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	gotTicket, gotPayload, err := DecodeRemoteComputeRequest(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotTicket != ticket {
		t.Fatalf("ticket mismatch:\n got %+v\nwant %+v", gotTicket, ticket)
	}
	if string(gotPayload) != string(payload) {
		t.Fatalf("payload mismatch: %q", gotPayload)
	}
}

func TestDecodeRejectsTamperedFrames(t *testing.T) {
	ticket := sampleTicket()
	frame, err := EncodeRemoteComputeRequest(ticket, []byte("payload"))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, _, err := DecodeRemoteComputeRequest(frame[:len(frame)-1]); err == nil {
		t.Fatal("truncated frame must fail")
	}
	tampered := append([]byte(nil), frame...)
	tampered[0] = remoteComputeWireVersion + 1
	if _, _, err := DecodeRemoteComputeRequest(tampered); err == nil {
		t.Fatal("unknown version must fail")
	}
}

// TestRemoteComputeHandlerLifecycle pins the requested→success/failed frame
// contract end to end, including correlation preservation and the checksum
// fidelity the remote executor depends on for verification.
func TestRemoteComputeHandlerLifecycle(t *testing.T) {
	ticket := sampleTicket()
	payload := []byte("compute-me")

	var seenTicket RemoteComputeTicket
	var seenPayload []byte
	executor := TicketExecutorFunc(func(_ context.Context, got RemoteComputeTicket, chunk []byte) ([]byte, error) {
		seenTicket = got
		seenPayload = append([]byte(nil), chunk...)
		return []byte("result"), nil
	})
	handler := RemoteComputeHandler(executor)

	requestFrame, err := EncodeRemoteComputeRequest(ticket, payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	response, err := handler(context.Background(), grpcsvc.Frame{
		EventType:     RemoteComputeRequestedEventType,
		Payload:       requestFrame,
		CorrelationID: "corr-42",
		SchemaVersion: "test.v1",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if response.EventType != RemoteComputeSuccessEventType {
		t.Fatalf("event type = %q want success", response.EventType)
	}
	if response.CorrelationID != "corr-42" {
		t.Fatalf("correlation = %q want corr-42", response.CorrelationID)
	}
	if string(response.Payload) != "result" {
		t.Fatalf("payload = %q", response.Payload)
	}
	if seenTicket.ChecksumHex != ticket.ChecksumHex || seenTicket.ChunkIndex != 4 {
		t.Fatalf("executor saw altered ticket: %+v", seenTicket)
	}
	if string(seenPayload) != string(payload) {
		t.Fatalf("executor payload = %q", seenPayload)
	}

	// Execution failure maps to a failed frame carrying the reason.
	failing := RemoteComputeHandler(TicketExecutorFunc(
		func(context.Context, RemoteComputeTicket, []byte) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
	))
	failed, err := failing(context.Background(), grpcsvc.Frame{
		EventType: RemoteComputeRequestedEventType, Payload: requestFrame, CorrelationID: "corr-43",
	})
	if err != nil {
		t.Fatalf("failed-frame path must not be a transport error: %v", err)
	}
	if failed.EventType != RemoteComputeFailedEventType {
		t.Fatalf("event type = %q want failed", failed.EventType)
	}
	if failed.CorrelationID != "corr-43" || !strings.Contains(string(failed.Payload), "deadline") {
		t.Fatalf("failed frame = %+v", failed)
	}

	// Malformed requests fail at the seam without reaching the executor.
	called := false
	guarded := RemoteComputeHandler(TicketExecutorFunc(
		func(context.Context, RemoteComputeTicket, []byte) ([]byte, error) {
			called = true
			return nil, nil
		},
	))
	bad, err := guarded(context.Background(), grpcsvc.Frame{
		EventType: RemoteComputeRequestedEventType, Payload: []byte("garbage"), CorrelationID: "corr-44",
	})
	if err != nil {
		t.Fatalf("malformed path: %v", err)
	}
	if called || bad.EventType != RemoteComputeFailedEventType {
		t.Fatalf("executor reached with garbage (called=%v) or wrong type %q", called, bad.EventType)
	}
}

// Error-path and boundary coverage: these legs pin the refusal contracts that
// keep malformed traffic from ever reaching a sink or executor.

func TestPlacementRefusalContracts(t *testing.T) {
	t.Run("publish rejects encode failures", func(t *testing.T) {
		bus := rediskit.NewMemoryClient("placement")
		if err := PublishLaneMirrors(context.Background(), bus, "", "", "", LaneClassEdge,
			[]LaneMirrorUpdate{{}}); err == nil || !strings.Contains(err.Error(), "node id") {
			t.Fatalf("empty node id err = %v", err)
		}
		tooMany := make([]LaneMirrorUpdate, 256)
		if err := PublishLaneMirrors(context.Background(), bus, "", "n", "r", LaneClassEdge, tooMany); err == nil ||
			!strings.Contains(err.Error(), "255 maximum") {
			t.Fatalf("oversized batch err = %v", err)
		}
	})

	t.Run("listener reports apply errors through the callback", func(t *testing.T) {
		bus := rediskit.NewMemoryClient("placement")
		failing := &failingSink{}
		ctx := t.Context()
		errs := make(chan error, 8)
		stop, err := ListenMirrors(ctx, bus, "", failing, func(_ int, cause error) { errs <- cause })
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer stop()

		want := LaneMirrorUpdate{Lane: 3, EwmaNs: 800, TickSeen: 12}
		if err := PublishLaneMirrors(ctx, bus, "", "n", "r", LaneClassEdge, []LaneMirrorUpdate{want}); err != nil {
			t.Fatalf("publish: %v", err)
		}
		select {
		case got := <-errs:
			if !errors.Is(got, errSinkRejected) {
				t.Fatalf("apply error = %v", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("apply error never surfaced")
		}
	})

	t.Run("validate rejects sub-floor and accepts zero ewma", func(t *testing.T) {
		subFloor := LaneMirrorUpdate{Lane: 1, EwmaNs: MinPlausibleEwmaNs - 1}
		if err := ValidateMirrorStats(subFloor); err == nil || !strings.Contains(err.Error(), "plausibility") {
			t.Fatalf("sub-floor err = %v", err)
		}
		if err := ValidateMirrorStats(LaneMirrorUpdate{Lane: 1}); err != nil {
			t.Fatalf("unsampled report must pass validation: %v", err)
		}
	})

	t.Run("mirror decode refuses unknown wire versions", func(t *testing.T) {
		frame, err := EncodeLaneMirrorFrame("n", "r", LaneClassHub, benchLanes()[:1])
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		frame[0] = MirrorWireVersion + 7
		if _, err := DecodeLaneMirrorFrame(frame); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("version err = %v", err)
		}
	})

	t.Run("remote ticket encode refuses bad checksum and long ids", func(t *testing.T) {
		badChecksum := sampleTicket()
		badChecksum.ChecksumHex = "zz"
		if _, err := EncodeRemoteComputeRequest(badChecksum, nil); err == nil ||
			!strings.Contains(err.Error(), "checksum") {
			t.Fatalf("bad checksum err = %v", err)
		}
		longID := sampleTicket()
		longID.ProjectionID = strings.Repeat("p", 256)
		if _, err := EncodeRemoteComputeRequest(longID, nil); err == nil ||
			!strings.Contains(err.Error(), "projection id") {
			t.Fatalf("long projection id err = %v", err)
		}
	})

	t.Run("remote decode walks truncation points without panicking", func(t *testing.T) {
		full, err := EncodeRemoteComputeRequest(sampleTicket(), []byte("payload"))
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		for cut := 0; cut < len(full); cut += 7 {
			if _, _, err := DecodeRemoteComputeRequest(full[:cut]); err != nil {
				continue
			}
			t.Fatalf("truncated frame at %d decoded without error", cut)
		}
	})

	t.Run("ticket validate names each missing field class", func(t *testing.T) {
		noClass := sampleTicket()
		noClass.RequiredClassMask = 0
		if err := noClass.Validate(); err == nil || !strings.Contains(err.Error(), "capability") {
			t.Fatalf("no-class err = %v", err)
		}
		noScope := sampleTicket()
		noScope.ScopeKey = ""
		if err := noScope.Validate(); err != nil {
			t.Fatalf("empty scope is advisory, not required: %v", err)
		}
	})
}

type failingSink struct{}

var errSinkRejected = errors.New("sink rejected update")

func (f *failingSink) ApplyMirrorUpdate(update LaneMirrorUpdate) error {
	return errSinkRejected
}

func TestFailedRemoteFrameCarriesReasonAndCorrelation(t *testing.T) {
	frame := failedRemoteFrame("corr-x", errors.New("boom"))
	if frame.EventType != RemoteComputeFailedEventType || frame.CorrelationID != "corr-x" {
		t.Fatalf("frame = %+v", frame)
	}
	if string(frame.Payload) != "boom" {
		t.Fatalf("payload = %q", frame.Payload)
	}
}

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
