//go:build perf

package grpcsvc

import (
	"context"
	"testing"
)

func TestDispatchFrameAllocBudget(t *testing.T) {
	conn, cleanup := startTestServer(t, ServerOptions{AuthToken: "secret", MaxMessageBytes: 64 * 1024})
	defer cleanup()

	jsonReq := Envelope{
		EventType:     "order:create:v1:requested",
		Payload:       map[string]any{"id": "ord_1"},
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	frameReq := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	if _, err := Dispatch(context.Background(), conn, jsonReq); err != nil {
		t.Fatalf("warmup Dispatch() error = %v", err)
	}
	if _, err := DispatchFrame(context.Background(), conn, frameReq); err != nil {
		t.Fatalf("warmup DispatchFrame() error = %v", err)
	}

	jsonAllocs := testing.AllocsPerRun(50, func() {
		if _, err := Dispatch(context.Background(), conn, jsonReq); err != nil {
			t.Fatalf("Dispatch() error = %v", err)
		}
	})
	frameAllocs := testing.AllocsPerRun(50, func() {
		if _, err := DispatchFrame(context.Background(), conn, frameReq); err != nil {
			t.Fatalf("DispatchFrame() error = %v", err)
		}
	})
	if frameAllocs >= jsonAllocs {
		t.Fatalf("binary frame path must allocate less than JSON compatibility path: frame=%0.1f json=%0.1f", frameAllocs, jsonAllocs)
	}
	if frameAllocs > 205 {
		t.Fatalf("binary frame allocation budget exceeded: got %0.1f allocs/run, want <= 205", frameAllocs)
	}
}

func TestRouterDispatchFrameDirectAllocBudget(t *testing.T) {
	client := NewDirectFrameClient(testRouter(t), ServerOptions{})
	frameReq := Frame{
		EventType:     "order:create:v1:frame",
		Payload:       []byte(`{"id":"ord_1"}`),
		CorrelationID: "corr_1",
		SchemaVersion: "1.0",
	}
	allocs := testing.AllocsPerRun(100, func() {
		if _, err := client.DispatchFrame(context.Background(), frameReq); err != nil {
			t.Fatalf("DispatchFrame() error = %v", err)
		}
	})
	if allocs > 1 {
		t.Fatalf("direct frame allocation budget exceeded: got %0.1f allocs/run, want <= 1", allocs)
	}
}
