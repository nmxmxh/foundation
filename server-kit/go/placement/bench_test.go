package placement

import (
	"context"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
)

func frameOf(eventType string, payload []byte) grpcsvc.Frame {
	return grpcsvc.Frame{EventType: eventType, Payload: payload, CorrelationID: "bench"}
}

// Benchmark fixtures build once outside the loop: these numbers attribute cost
// to the codec and the decision function, not to fixture construction
// (TE-40.3). Results are sunk so the compiler cannot delete the work.
func benchLanes() []LaneMirrorUpdate {
	lanes := make([]LaneMirrorUpdate, 32)
	for i := range lanes {
		lanes[i] = LaneMirrorUpdate{
			Lane:           uint16(i),
			Jurisdiction:   uint16(i % 8),
			MaxConcurrency: 8,
			Inflight:       uint32(i % 4),
			Generation:     uint32(i + 1),
			Class:          LaneClassHub,
			UnitClassMask:  0b11,
			AffinityBloom:  1 << (uint64(i) % 64),
			EwmaNs:         1_000 + uint64(i)*100,
			TickSeen:       uint64(1_000 + i),
		}
	}
	return lanes
}

func BenchmarkMirrorFrameEncode32(b *testing.B) {
	lanes := benchLanes()
	var sink []byte
	for b.Loop() {
		frame, err := EncodeLaneMirrorFrame("bench-node", "bench-region", LaneClassHub, lanes)
		if err != nil {
			b.Fatalf("encode: %v", err)
		}
		sink = frame
	}
	_ = sink
}

func BenchmarkMirrorFrameDecode32(b *testing.B) {
	lanes := benchLanes()
	frame, err := EncodeLaneMirrorFrame("bench-node", "bench-region", LaneClassHub, lanes)
	if err != nil {
		b.Fatalf("encode: %v", err)
	}
	b.ResetTimer()
	var sink DecodedMirrorFrame
	for b.Loop() {
		decoded, err := DecodeLaneMirrorFrame(frame)
		if err != nil {
			b.Fatalf("decode: %v", err)
		}
		sink = decoded
	}
	if len(sink.Lanes) != 32 {
		b.Fatalf("sink lost lanes: %d", len(sink.Lanes))
	}
}

// BenchmarkRemoteComputeRoundTrip covers ticket encode, handler validation,
// execution, and response framing — the full seam a remote executor call pays.
func BenchmarkRemoteComputeRoundTrip(b *testing.B) {
	ticket := TicketFromChunkDescriptor(
		"orders.projection", "orders:org-1",
		testChunk(1, 4096),
		0b01, 0, 250_000,
	)
	request, err := EncodeRemoteComputeRequest(ticket, make([]byte, 4096))
	if err != nil {
		b.Fatalf("encode: %v", err)
	}
	handler := RemoteComputeHandler(TicketExecutorFunc(
		func(_ context.Context, _ RemoteComputeTicket, payload []byte) ([]byte, error) {
			return payload, nil
		},
	))
	var sink int
	for b.Loop() {
		response, err := handler(context.Background(), frameOf(RemoteComputeRequestedEventType, request))
		if err != nil {
			b.Fatalf("handler: %v", err)
		}
		sink += len(response.Payload)
	}
	if sink == 0 {
		b.Fatal("sink emptied")
	}
}
