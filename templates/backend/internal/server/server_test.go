package server

import (
	"context"
	"testing"

	"github.com/gorilla/websocket"
)

func TestServerScaffoldPackageCompiles(t *testing.T) {
	// This smoke test keeps the scaffold server package in the unit-test set
	// without assuming a project has preserved the baseline constructor shape.
}

func newTestWSRuntimeServer(maxConnections int) *Server {
	return &Server{
		wsMaxConnections: maxConnections,
		ws:               newWSRuntime(),
	}
}

func TestReserveWSConnectionSlotRejectsWhenCapacityExceeded(t *testing.T) {
	srv := newTestWSRuntimeServer(1)
	srv.ws.connectionCnt.Store(1)

	if srv.reserveWSConnectionSlot() {
		t.Fatal("expected capacity rejection")
	}
	if got := srv.ws.connectionCnt.Load(); got != 1 {
		t.Fatalf("connection count drifted after rejection: %d", got)
	}
	if _, ok := srv.ws.connections.Load("overflow"); ok {
		t.Fatal("rejected connection was stored")
	}
	if got := srv.ws.metrics.Snapshot().ConnectionsRejected; got != 1 {
		t.Fatalf("rejected metric = %d, want 1", got)
	}
}

func TestRegisterWSConnectionUsesReservedSlot(t *testing.T) {
	srv := newTestWSRuntimeServer(1)
	if !srv.reserveWSConnectionSlot() {
		t.Fatal("reserve slot failed")
	}

	registered := srv.registerWSConnection(context.Background(), &wsConnection{
		id:       "accepted",
		deviceID: "accepted-device",
		reserved: true,
	})

	if !registered {
		t.Fatal("expected reserved connection to register")
	}
	if got := srv.ws.connectionCnt.Load(); got != 1 {
		t.Fatalf("connection count = %d, want 1", got)
	}
	if got := srv.ws.metrics.Snapshot().ConnectionsTotal; got != 1 {
		t.Fatalf("connection total metric = %d, want 1", got)
	}
}

func TestEnqueueWSRecordsBackpressureFailure(t *testing.T) {
	srv := newTestWSRuntimeServer(1)
	conn := &wsConnection{
		id:   "blocked",
		send: make(chan wsOutbound),
	}

	err := srv.enqueueWS(conn, websocket.TextMessage, []byte("payload"))

	if err == nil {
		t.Fatal("expected queue-full error")
	}
	if got := srv.ws.metrics.Snapshot().MessagesFailed; got != 1 {
		t.Fatalf("failed message metric = %d, want 1", got)
	}
}
