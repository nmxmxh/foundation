//go:build cgo && (linux || darwin)

package runtimehost

import (
	"bytes"
	"context"
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
			buffer := make([]byte, generated.BUFFER_TOTAL_BYTES)
			return &buffer
		}},
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
