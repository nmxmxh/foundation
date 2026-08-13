//go:build cgo && (linux || darwin)

package runtimehost

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

func TestFFIPoolBackendSeamOrchestratesRuntimeBuffer(t *testing.T) {
	backend := &scriptedFFIBackend{
		process: func(unitID string, raw []byte, _ []byte) (int32, string) {
			if unitID != "runtime.echo" {
				return 1, "unexpected unit"
			}
			buffer, err := NewBuffer(raw)
			if err != nil {
				return 1, err.Error()
			}
			input, err := buffer.InputBytesView()
			if err != nil {
				return 1, err.Error()
			}
			if err := buffer.SetOutputBytesFast(bytes.ToUpper(input)); err != nil {
				return 1, err.Error()
			}
			_, _ = buffer.AddEpoch(generated.IDX_OUTPUT_WRITTEN, 1)
			return 0, ""
		},
	}
	pool := newTestFFIPool(backend)
	response, err := pool.Execute(context.Background(), ProcessRequest{
		UnitID:        "runtime.echo",
		Input:         []byte("ffi seam"),
		ContextHash:   42,
		ModuleVersion: 7,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if string(response.Output) != "FFI SEAM" || response.OutputEpoch != 1 {
		t.Fatalf("response = %+v", response)
	}
	if backend.calls != 1 {
		t.Fatalf("backend calls = %d", backend.calls)
	}
}

func TestFFIPoolBackendSeamErrorAndClosePaths(t *testing.T) {
	backend := &scriptedFFIBackend{processStatus: 1, processMessage: "backend failed"}
	pool := newTestFFIPool(backend)
	if _, err := pool.Execute(context.Background(), ProcessRequest{UnitID: "runtime.fail"}); err == nil || !strings.Contains(err.Error(), "backend failed") {
		t.Fatalf("Execute() error = %v", err)
	}
	backend.processMessage = ""
	if _, err := pool.Execute(context.Background(), ProcessRequest{UnitID: "runtime.fail"}); err == nil || !strings.Contains(err.Error(), "ffi runtime process failed") {
		t.Fatalf("Execute(default message) error = %v", err)
	}
	backend.closeErr = errors.New("close failed")
	if err := pool.Close(); err == nil || !strings.Contains(err.Error(), "close failed") {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := pool.Execute(context.Background(), ProcessRequest{UnitID: "runtime.closed"}); err == nil || !strings.Contains(err.Error(), "ffi runtime host is closed") {
		t.Fatalf("Execute(closed) error = %v", err)
	}
}

func TestFFIPoolBackendSeamRuntimeStatusAndNilContext(t *testing.T) {
	backend := &scriptedFFIBackend{
		process: func(_ string, raw []byte, _ []byte) (int32, string) {
			buffer, err := NewBuffer(raw)
			if err != nil {
				return 1, err.Error()
			}
			_ = buffer.SetHeaderInt(generated.INT_IDX_STATUS_CODE, 7)
			_ = buffer.SetDiagnosticsText("runtime status failed")
			return 0, ""
		},
	}
	pool := newTestFFIPool(backend)
	response, err := pool.Execute(nilContext(), ProcessRequest{UnitID: "runtime.status"})
	if err == nil || response.StatusCode != 7 || response.Diagnostics != "runtime status failed" {
		t.Fatalf("Execute(nil ctx) response=%+v err=%v", response, err)
	}
}

func TestCgoFFIBackendDirectProcessAndClose(t *testing.T) {
	libraryPath := buildFFITestLibrary(t)
	backend, err := openFFIBackend(libraryPath, 1)
	if err != nil {
		t.Fatalf("openFFIBackend() error = %v", err)
	}

	raw := newRuntimeBuffer(t, "adapter")
	var errBuf [4096]byte
	status, message := backend.Process("runtime.echo", raw, errBuf[:])
	if status != 0 || message != "" {
		t.Fatalf("Process(echo) status=%d message=%q", status, message)
	}
	buffer, err := NewBuffer(raw)
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}
	output, err := buffer.OutputBytesView()
	if err != nil || string(output) != "ADAPTER" {
		t.Fatalf("output=%q err=%v", string(output), err)
	}

	status, message = backend.Process("runtime.echo", raw[:len(raw)-1], errBuf[:])
	if status == 0 || !strings.Contains(message, "invalid runtime buffer") {
		t.Fatalf("Process(short buffer) status=%d message=%q", status, message)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close() idempotent error = %v", err)
	}
	status, message = backend.Process("runtime.echo", raw, errBuf[:])
	if status == 0 || !strings.Contains(message, "ffi runtime host is closed") {
		t.Fatalf("Process(closed) status=%d message=%q", status, message)
	}
	if err := (*cgoFFIBackend)(nil).Close(); err != nil {
		t.Fatalf("nil backend Close() error = %v", err)
	}
}

func nilContext() context.Context {
	return nil
}

func newTestFFIPool(backend ffiBackend) *FFIPool {
	return &FFIPool{
		backend: backend,
		bufferPool: sync.Pool{New: func() any {
			buffer, err := NewBuffer(make([]byte, generated.BUFFER_TOTAL_BYTES))
			if err != nil {
				return nil
			}
			return buffer
		}},
		errorPool: sync.Pool{New: func() any {
			buffer := make([]byte, ffiErrorBufferBytes)
			return &buffer
		}},
	}
}

// echoFFIBackend copies input to output straight through the raw buffer.
//
// Deliberately avoids NewBuffer and bytes.ToUpper. The allocation test measures
// the pool, and anything the fake allocates would be counted against it — a
// real cgo backend crosses into C and allocates nothing on the Go heap, so a
// fake that does is not standing in for it.
func echoFFIBackend() *scriptedFFIBackend {
	word := func(raw []byte, offset uint32) []byte { return raw[offset : offset+4] }
	return &scriptedFFIBackend{
		process: func(_ string, raw []byte, _ []byte) (int32, string) {
			inputLen := binary.LittleEndian.Uint32(
				word(raw, generated.OFFSET_HEADER_INTS+generated.INT_IDX_INPUT_LENGTH*4))
			if inputLen > generated.INPUT_MAX_BYTES {
				return 1, "input length out of range"
			}
			input := raw[generated.OFFSET_INPUT_BYTES : generated.OFFSET_INPUT_BYTES+inputLen]
			copy(raw[generated.OFFSET_OUTPUT_BYTES:], input)
			binary.LittleEndian.PutUint32(
				word(raw, generated.OFFSET_HEADER_INTS+generated.INT_IDX_OUTPUT_LENGTH*4), inputLen)

			epoch := word(raw, generated.OFFSET_EPOCHS+generated.IDX_OUTPUT_WRITTEN*generated.EPOCH_SLOT_BYTES)
			binary.LittleEndian.PutUint32(epoch, binary.LittleEndian.Uint32(epoch)+1)
			return 0, ""
		},
	}
}

// The FFI lane's argument is that it does not copy across the boundary. A call
// that allocates twice on the way out — an output copy and a 4 KiB error buffer
// that is almost never written — spends most of that argument before returning.
func TestFFIPoolExecuteIntoAllocatesNothingInSteadyState(t *testing.T) {
	pool := newTestFFIPool(echoFFIBackend())
	dst := make([]byte, generated.OUTPUT_MAX_BYTES)
	req := ProcessRequest{UnitID: "runtime.echo", Input: []byte("ffi seam")}

	// Prime the pools; the first call through a sync.Pool always allocates.
	if _, err := pool.ExecuteInto(context.Background(), req, dst); err != nil {
		t.Fatalf("ExecuteInto() error = %v", err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		response, err := pool.ExecuteInto(context.Background(), req, dst)
		if err != nil || string(response.Output) != "ffi seam" {
			t.Fatalf("ExecuteInto() output=%q err=%v", response.Output, err)
		}
	})
	if allocs > 0 {
		t.Errorf("ExecuteInto allocated %.0f times per call, want 0", allocs)
	}
}

// A short destination must fail rather than truncate. Units return packed
// binary records, so a short result decodes as a valid shorter one — the
// failure would surface as quietly missing data instead of an error.
func TestFFIPoolExecuteIntoRefusesAShortDestination(t *testing.T) {
	pool := newTestFFIPool(echoFFIBackend())
	_, err := pool.ExecuteInto(context.Background(), ProcessRequest{
		UnitID: "runtime.echo",
		Input:  []byte("more than four bytes"),
	}, make([]byte, 4))
	if err == nil || !strings.Contains(err.Error(), "destination holds 4") {
		t.Fatalf("ExecuteInto() into a short destination error = %v", err)
	}
}

func TestFFIPoolExecuteIntoRequiresADestination(t *testing.T) {
	pool := newTestFFIPool(echoFFIBackend())
	if _, err := pool.ExecuteInto(context.Background(), ProcessRequest{UnitID: "runtime.echo"}, nil); err == nil {
		t.Fatal("ExecuteInto() with a nil destination must fail")
	}
}

type scriptedFFIBackend struct {
	process        func(string, []byte, []byte) (int32, string)
	processStatus  int32
	processMessage string
	closeErr       error
	calls          int
}

func (b *scriptedFFIBackend) Process(unitID string, buffer []byte, errBuf []byte) (int32, string) {
	b.calls++
	if b.process != nil {
		return b.process(unitID, buffer, errBuf)
	}
	if b.processMessage != "" {
		copy(errBuf, b.processMessage)
	}
	return b.processStatus, b.processMessage
}

func (b *scriptedFFIBackend) Close() error {
	return b.closeErr
}
