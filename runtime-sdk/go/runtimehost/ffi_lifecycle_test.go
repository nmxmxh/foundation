//go:build cgo && (linux || darwin)

package runtimehost

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFFIPoolCloseWaitsForActiveCalls(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	backend := &scriptedFFIBackend{process: func(_ string, _ []byte, _ []byte) (int32, string) {
		close(entered)
		select {
		case <-release:
			return 0, ""
		case <-time.After(3 * time.Second):
			return 1, "test release timed out"
		}
	}}
	pool := newTestFFIPool(backend)
	executed, closed := make(chan error, 1), make(chan error, 2)
	go func() {
		_, err := pool.Execute(context.Background(), ProcessRequest{UnitID: "blocking"})
		executed <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("native call did not start")
	}
	go func() { closed <- pool.Close() }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		pool.mu.RLock()
		closing := pool.closeDone != nil
		pool.mu.RUnlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("close did not stop admission")
		}
		runtime.Gosched()
	}
	go func() { closed <- pool.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("library closed during native call: %v", err)
	default:
	}
	if _, err := pool.Execute(context.Background(), ProcessRequest{UnitID: "new"}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closing pool accepted work: %v", err)
	}
	close(release)
	for range 2 {
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("close did not complete")
		}
	}
	if err := <-executed; err != nil {
		t.Fatal(err)
	}
	if backend.closeCalls != 1 {
		t.Fatalf("backend close calls: got %d, want 1", backend.closeCalls)
	}
}
