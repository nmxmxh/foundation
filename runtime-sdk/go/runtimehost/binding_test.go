//go:build (darwin || linux) && cgo

package runtimehost

import (
	"context"
	"errors"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/placement"
)

func TestFFIBindingAdapter(t *testing.T) {
	pool := newTestFFIPool(echoFFIBackend())
	spec := placement.BindingSpec{MaxInputBytes: 8, MaxOutputBytes: 8}
	bound, err := NewFFIBinding(pool, "runtime.echo", spec)
	if err != nil {
		t.Fatal(err)
	}
	if bound.InputCopies != 1 {
		t.Fatal("FFI copy was concealed")
	}
	var output [8]byte
	written, err := bound.Operation(context.Background(), []byte("resident"), output[:])
	if err != nil || written != 8 || string(output[:]) != "resident" {
		t.Fatalf("native binding: %d %q %v", written, output, err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := bound.Operation(context.Background(), []byte("resident"), output[:]); err == nil {
		t.Fatal("closed native pool accepted work")
	}
	if _, err := NewFFIBinding(nil, "unit", spec); !errors.Is(err, placement.ErrBindingInvalid) {
		t.Fatal(err)
	}
	for _, unit := range []string{"", string(make([]byte, 129))} {
		if _, err := NewFFIBinding(pool, unit, spec); err == nil {
			t.Fatal("invalid unit accepted")
		}
	}
	for _, bad := range []placement.BindingSpec{{}, {MaxInputBytes: 1 << 20, MaxOutputBytes: 8}, {MaxInputBytes: 8, MaxOutputBytes: 1 << 20}} {
		if _, err := NewFFIBinding(pool, "unit", bad); !errors.Is(err, placement.ErrBindingInvalid) {
			t.Fatal(err)
		}
	}
}
