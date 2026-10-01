package pushx

import (
	"context"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

func TestPushAdapter(t *testing.T) {
	r, err := New(&storeStub{}, senderFunc(nil), DefaultOptions())
	if err != nil {
		t.Fatalf("failed to create runner: %v", err)
	}

	adp := NewAdapter("custom-push", r)
	if adp.Name() != "custom-push" {
		t.Fatalf("expected name custom-push, got %s", adp.Name())
	}
	if adp.Kind() != adapter.KindPush {
		t.Fatalf("expected kind push, got %s", adp.Kind())
	}

	h, err := adp.Probe(context.Background())
	if err != nil || h != adapter.HealthServing {
		t.Fatalf("expected HealthServing, got %v (err: %v)", h, err)
	}

	st := adp.Status()
	if st.Health != adapter.HealthServing {
		t.Fatalf("expected status health serving, got %v", st.Health)
	}

	if err := adp.Close(); err != nil {
		t.Fatalf("failed to close adapter: %v", err)
	}

	if adp.Runner() != r {
		t.Fatal("unexpected runner pointer")
	}

	// Nil runner case.
	nilAdp := NewAdapter("", nil)
	if nilAdp.Name() != "push" {
		t.Fatalf("default name should be push, got %s", nilAdp.Name())
	}
	nh, _ := nilAdp.Probe(context.Background())
	if nh != adapter.HealthNotServing {
		t.Fatalf("expected HealthNotServing for nil runner, got %v", nh)
	}
}
