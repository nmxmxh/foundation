package placement

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)

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
		if err := ListenMirrors(ctx, bus, "", failing, func(_ int, cause error) { errs <- cause }); err != nil {
			t.Fatalf("listen: %v", err)
		}

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
