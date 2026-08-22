package placement

import (
	"context"
	"strings"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
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
