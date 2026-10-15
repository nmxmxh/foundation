//go:build foundation_movement && cgo && (linux || darwin)

package runtimehost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/placement"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

func movementFFIPool(tb testing.TB, library string) *FFIPool {
	tb.Helper()
	pool, err := NewFFIPool(FFIPoolOptions{LibraryPath: library, Workers: 1})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := pool.Close(); err != nil {
			tb.Error(err)
		}
	})
	return pool
}

func captureMovement(tb testing.TB, operation func()) movementSnapshot {
	tb.Helper()
	collector, ok := startMovementCollection()
	if !ok {
		tb.Fatal("movement collection already active")
	}
	defer func() {
		if !collector.stop() {
			tb.Error("movement collector ownership changed")
		}
	}()
	operation()
	return collector.snapshot()
}

func ffiMovementBudget(input, output int) movementSnapshot {
	return movementSnapshot{uint64(input), uint64(output), uint64(generated.BUFFER_TOTAL_BYTES), ffiErrorBufferBytes}
}

func TestMovementFFIExecuteInto(t *testing.T) {
	pool := movementFFIPool(t, buildFFITestLibrary(t))
	for _, size := range []int{0, 8, 64, 256, int(generated.INPUT_MAX_BYTES)} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			input := bytes.Repeat([]byte("a"), size)
			dst := make([]byte, generated.OUTPUT_MAX_BYTES)
			got := captureMovement(t, func() {
				response, err := pool.ExecuteInto(t.Context(), ProcessRequest{UnitID: "runtime.echo", Input: input}, dst)
				if err != nil || !bytes.Equal(response.Output, bytes.Repeat([]byte("A"), size)) {
					t.Fatalf("native output: %q %v", response.Output, err)
				}
				if size > 0 && &response.Output[0] != &dst[0] {
					t.Fatal("output does not use caller storage")
				}
			})
			if want := ffiMovementBudget(size, size); got != want {
				t.Fatalf("movement: %+v, want %+v", got, want)
			}
			t.Logf("host bytes: %+v", got)
		})
	}
}

func TestMovementFFIFailures(t *testing.T) {
	library := buildFFITestLibrary(t)
	for _, name := range []string{"canceled", "nil-output", "oversized", "short-output", "runtime-status", "closed"} {
		t.Run(name, func(t *testing.T) {
			pool := movementFFIPool(t, library)
			ctx := t.Context()
			request := ProcessRequest{UnitID: "runtime.echo", Input: []byte("eight123")}
			dst := make([]byte, generated.OUTPUT_MAX_BYTES)
			want := ffiMovementBudget(8, 0)
			errorText := ""
			switch name {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want, errorText = movementSnapshot{}, "context canceled"
			case "nil-output":
				dst = nil
				want, errorText = movementSnapshot{}, "destination is required"
			case "oversized":
				request.Input = make([]byte, generated.INPUT_MAX_BYTES+1)
				want = movementSnapshot{controlCleared: uint64(generated.BUFFER_TOTAL_BYTES)}
				errorText = "input payload too large"
			case "short-output":
				dst, errorText = dst[:1], "destination holds 1"
			case "runtime-status":
				request.UnitID, errorText = "runtime.fail", "forced ffi failure"
			case "closed":
				if err := pool.Close(); err != nil {
					t.Fatal(err)
				}
				want.errorCleared, errorText = 0, "host is closed"
			}
			got := captureMovement(t, func() {
				if _, err := pool.ExecuteInto(ctx, request, dst); err == nil || !strings.Contains(err.Error(), errorText) {
					t.Fatalf("expected %q, got %v", errorText, err)
				}
			})
			if got != want {
				t.Fatalf("failure movement: %+v, want %+v", got, want)
			}
		})
	}
}

func TestMovementFFIBindingReceipt(t *testing.T) {
	pool := movementFFIPool(t, buildFFITestLibrary(t))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ctx = security.ContextWithOrganizationID(ctx, "movement-tenant")
	md := metadata.New()
	md.CorrelationID = "movement-binding"
	ctx = metadata.IntoContext(ctx, md)
	validate := func(data []byte) error {
		if len(data) != 8 {
			return placement.ErrBindingInvalid
		}
		return nil
	}
	registry, err := placement.NewBindingRegistry(1, placement.BindingLimits{Resources: 1, Bindings: 1, ResidentBytes: 16, InFlight: 1},
		func(context.Context, placement.BindingAction, uint64, uint64) error { return nil }, placement.BindingType{ID: 1, Validate: validate})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := registry.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := registry.PublishResource(ctx, 1, 1, 0, []byte("resident")); err != nil {
		t.Fatal(err)
	}
	spec, err := NewFFIBinding(pool, "runtime.echo", placement.BindingSpec{ID: 1, ResourceID: 1, ResourceGeneration: 1,
		InputTypeID: 1, OutputTypeID: 1, MaxInputBytes: 8, MaxOutputBytes: 8, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	request, err := registry.Bind(ctx, spec, 0)
	if err != nil {
		t.Fatal(err)
	}
	var output [8]byte
	var receipt placement.RuntimeBindingReceipt
	got := captureMovement(t, func() {
		receipt, err = registry.Execute(ctx, request, "movement-binding", output[:])
	})
	if err != nil || string(output[:]) != "RESIDENT" || got != ffiMovementBudget(8, 8) || receipt.InputCopiedBytes != got.inputCopied {
		t.Fatalf("receipt=%+v observed=%+v output=%q error=%v", receipt, got, output, err)
	}
	request.MaxInputCopyBytes = 0
	got = captureMovement(t, func() {
		_, err = registry.Execute(ctx, request, "movement-binding", output[:])
	})
	if !errors.Is(err, placement.ErrBindingBudget) || got != (movementSnapshot{}) {
		t.Fatalf("copy admission: movement=%+v error=%v", got, err)
	}
}

func TestMovementFFIExtraCopyExceedsBudget(t *testing.T) {
	pool := movementFFIPool(t, buildFFITestLibrary(t))
	input := []byte("resident")
	var extra, output [8]byte
	got := captureMovement(t, func() {
		movementRecordInputCopy(copy(extra[:], input))
		if _, err := pool.ExecuteInto(t.Context(), ProcessRequest{UnitID: "runtime.echo", Input: extra[:]}, output[:]); err != nil {
			t.Fatal(err)
		}
	})
	if want := ffiMovementBudget(8, 8); got == want || got.inputCopied != 2*want.inputCopied {
		t.Fatalf("extra copy did not exceed the input budget: %+v", got)
	}
}

func BenchmarkMovementFFIExecuteInto(b *testing.B) {
	library := buildFFITestLibrary(b)
	for _, size := range []int{8, 64, 256, int(generated.INPUT_MAX_BYTES)} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			pool := movementFFIPool(b, library)
			input := bytes.Repeat([]byte("a"), size)
			dst := make([]byte, generated.OUTPUT_MAX_BYTES)
			request := ProcessRequest{UnitID: "runtime.echo", Input: input}
			if _, err := pool.ExecuteInto(b.Context(), request, dst); err != nil {
				b.Fatal(err)
			}
			collector, ok := startMovementCollection()
			if !ok {
				b.Fatal("movement collection already active")
			}
			defer collector.stop()
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for b.Loop() {
				if _, err := pool.ExecuteInto(b.Context(), request, dst); err != nil {
					b.Fatal(err)
				}
			}
			counts := collector.snapshot()
			b.ReportMetric(float64(counts.inputCopied)/float64(b.N), "input-copy-B/op")
			b.ReportMetric(float64(counts.outputCopied)/float64(b.N), "output-copy-B/op")
			b.ReportMetric(float64(counts.controlCleared)/float64(b.N), "control-clear-B/op")
			b.ReportMetric(float64(counts.errorCleared)/float64(b.N), "error-clear-B/op")
		})
	}
}
