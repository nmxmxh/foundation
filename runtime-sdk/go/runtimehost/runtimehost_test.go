package runtimehost

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

func TestBufferRoundTrip(t *testing.T) {
	raw := make([]byte, generated.BUFFER_TOTAL_BYTES)
	buffer, err := NewBuffer(raw)
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}

	if err := buffer.Initialize(4); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := buffer.SetInputBytes([]byte("asset")); err != nil {
		t.Fatalf("SetInputBytes() error = %v", err)
	}
	if err := buffer.SetOutputBytes([]byte("layout")); err != nil {
		t.Fatalf("SetOutputBytes() error = %v", err)
	}

	input, err := buffer.InputBytes()
	if err != nil {
		t.Fatalf("InputBytes() error = %v", err)
	}
	if string(input) != "asset" {
		t.Fatalf("unexpected input payload %q", string(input))
	}
	inputView, err := buffer.InputBytesView()
	if err != nil {
		t.Fatalf("InputBytesView() error = %v", err)
	}
	if string(inputView) != "asset" {
		t.Fatalf("unexpected input view payload %q", string(inputView))
	}

	output, err := buffer.OutputBytes()
	if err != nil {
		t.Fatalf("OutputBytes() error = %v", err)
	}
	if string(output) != "layout" {
		t.Fatalf("unexpected output payload %q", string(output))
	}
	outputView, err := buffer.OutputBytesView()
	if err != nil {
		t.Fatalf("OutputBytesView() error = %v", err)
	}
	if string(outputView) != "layout" {
		t.Fatalf("unexpected output view payload %q", string(outputView))
	}

	if err := buffer.SetDiagnosticsText("degraded"); err != nil {
		t.Fatalf("SetDiagnosticsText() error = %v", err)
	}
	if buffer.DiagnosticsText() != "degraded" {
		t.Fatalf("unexpected diagnostics payload %q", buffer.DiagnosticsText())
	}
}

func TestBufferFastSettersUseDeclaredLength(t *testing.T) {
	raw := make([]byte, generated.BUFFER_TOTAL_BYTES)
	buffer, err := NewBuffer(raw)
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}
	buffer.Reset()
	if err := buffer.Initialize(1); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	if err := buffer.SetInputBytes(bytes.Repeat([]byte{'x'}, 16)); err != nil {
		t.Fatalf("SetInputBytes() error = %v", err)
	}
	if err := buffer.SetInputBytesFast([]byte("in")); err != nil {
		t.Fatalf("SetInputBytesFast() error = %v", err)
	}
	input, err := buffer.InputBytesView()
	if err != nil || string(input) != "in" {
		t.Fatalf("InputBytesView() = %q err=%v", string(input), err)
	}

	if err := buffer.SetOutputBytes(bytes.Repeat([]byte{'y'}, 16)); err != nil {
		t.Fatalf("SetOutputBytes() error = %v", err)
	}
	if err := buffer.SetOutputBytesFast([]byte("out")); err != nil {
		t.Fatalf("SetOutputBytesFast() error = %v", err)
	}
	output, err := buffer.OutputBytesView()
	if err != nil || string(output) != "out" {
		t.Fatalf("OutputBytesView() = %q err=%v", string(output), err)
	}
}

func TestBufferBoundsAndReset(t *testing.T) {
	if _, err := NewBuffer(make([]byte, generated.BUFFER_TOTAL_BYTES-1)); err == nil {
		t.Fatal("expected undersized buffer to fail")
	}
	buffer, err := NewBuffer(make([]byte, generated.BUFFER_TOTAL_BYTES))
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}
	if buffer.RawBytes() == nil {
		t.Fatal("expected raw bytes")
	}
	if got := (*Buffer)(nil).RawBytes(); got != nil {
		t.Fatalf("nil RawBytes() = %+v, want nil", got)
	}
	(*Buffer)(nil).Reset()

	if _, err := buffer.HeaderInt(generated.HEADER_INT_COUNT); err == nil {
		t.Fatal("expected invalid header index to fail")
	}
	if err := buffer.SetHeaderInt(generated.HEADER_INT_COUNT, 1); err == nil {
		t.Fatal("expected invalid header write to fail")
	}
	if got := buffer.LoadEpoch(generated.EPOCH_SLOT_COUNT); got != 0 {
		t.Fatalf("invalid epoch load = %d, want 0", got)
	}
	if err := buffer.StoreEpoch(generated.EPOCH_SLOT_COUNT, 1); err == nil {
		t.Fatal("expected invalid epoch store to fail")
	}
	if _, err := buffer.AddEpoch(generated.EPOCH_SLOT_COUNT, 1); err == nil {
		t.Fatal("expected invalid epoch add to fail")
	}

	if err := buffer.SetInputBytes(make([]byte, generated.INPUT_MAX_BYTES+1)); err == nil {
		t.Fatal("expected oversized input to fail")
	}
	if err := buffer.SetInputBytesFast(make([]byte, generated.INPUT_MAX_BYTES+1)); err == nil {
		t.Fatal("expected oversized fast input to fail")
	}
	if err := buffer.SetOutputBytes(make([]byte, generated.OUTPUT_MAX_BYTES+1)); err == nil {
		t.Fatal("expected oversized output to fail")
	}
	if err := buffer.SetOutputBytesFast(make([]byte, generated.OUTPUT_MAX_BYTES+1)); err == nil {
		t.Fatal("expected oversized fast output to fail")
	}
	if err := buffer.SetHeaderInt(generated.INT_IDX_INPUT_LENGTH, -1); err != nil {
		t.Fatalf("SetHeaderInt(input length) error = %v", err)
	}
	if _, err := buffer.InputBytes(); err == nil {
		t.Fatal("expected negative input length to fail")
	}
	if _, err := buffer.InputBytesView(); err == nil {
		t.Fatal("expected negative input view length to fail")
	}
	if err := buffer.SetHeaderInt(generated.INT_IDX_OUTPUT_LENGTH, int32(generated.OUTPUT_MAX_BYTES+1)); err != nil {
		t.Fatalf("SetHeaderInt(output length) error = %v", err)
	}
	if _, err := buffer.OutputBytes(); err == nil {
		t.Fatal("expected oversized output length to fail")
	}
	if _, err := buffer.OutputBytesView(); err == nil {
		t.Fatal("expected oversized output view length to fail")
	}
	if err := buffer.SetDiagnosticsText(strings.Repeat("x", int(generated.DIAGNOSTIC_MAX_BYTES)+1)); err == nil {
		t.Fatal("expected oversized diagnostics to fail")
	}
	buffer.Reset()
	if buffer.DiagnosticsText() != "" {
		t.Fatalf("DiagnosticsText() = %q, want empty after reset", buffer.DiagnosticsText())
	}
}

func TestColumnarArenaDescriptorContract(t *testing.T) {
	if generated.ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH != 5 {
		t.Fatalf("columnar batch descriptor type = %d, want 5", generated.ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH)
	}
	if generated.COLUMNAR_BATCH_HEADER_BYTES != 32 {
		t.Fatalf("columnar batch header bytes = %d, want 32", generated.COLUMNAR_BATCH_HEADER_BYTES)
	}
	if generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES != 64 {
		t.Fatalf("columnar field descriptor bytes = %d, want 64", generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES)
	}
	if generated.COLUMNAR_BATCH_ALIGNMENT_BYTES != 64 {
		t.Fatalf("columnar alignment bytes = %d, want 64", generated.COLUMNAR_BATCH_ALIGNMENT_BYTES)
	}
	if generated.COLUMNAR_BATCH_HEADER_BYTES%4 != 0 || generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES%4 != 0 {
		t.Fatal("columnar descriptor units must stay u32-aligned")
	}

	columnCount := uint32(2)
	payloadBytes := generated.COLUMNAR_BATCH_HEADER_BYTES + columnCount*generated.COLUMNAR_FIELD_DESCRIPTOR_BYTES
	if payloadBytes%generated.COLUMNAR_BATCH_ALIGNMENT_BYTES != 32 {
		t.Fatalf("unexpected unpadded payload modulo: %d", payloadBytes%generated.COLUMNAR_BATCH_ALIGNMENT_BYTES)
	}
	paddedBytes := ((payloadBytes + generated.COLUMNAR_BATCH_ALIGNMENT_BYTES - 1) / generated.COLUMNAR_BATCH_ALIGNMENT_BYTES) * generated.COLUMNAR_BATCH_ALIGNMENT_BYTES
	if paddedBytes != 192 {
		t.Fatalf("padded columnar descriptor bytes = %d, want 192", paddedBytes)
	}
	if generated.COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID != 8 {
		t.Fatalf("values descriptor slot = %d, want 8", generated.COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID)
	}
	if generated.COLUMNAR_DESCRIPTOR_ID_NONE != ^uint32(0) {
		t.Fatalf("none descriptor sentinel = %d, want max uint32", generated.COLUMNAR_DESCRIPTOR_ID_NONE)
	}
}

func TestRuntimeUnitDescriptorValidation(t *testing.T) {
	descriptor := RuntimeUnitDescriptor{
		UnitID:               "preview.compute",
		Role:                 RuntimeRoleCompute,
		InputSchema:          "media/v1/asset.capnp",
		OutputSchema:         "preview/v1/layout.capnp",
		SupportsWASM:         true,
		SupportsNative:       true,
		RequiresSharedMemory: true,
		MaxConcurrency:       1,
	}
	if err := descriptor.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, invalid := range []RuntimeUnitDescriptor{
		{},
		{UnitID: "unit"},
		{UnitID: "unit", InputSchema: "in"},
		{UnitID: "unit", InputSchema: "in", OutputSchema: "out"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("expected invalid descriptor to fail: %+v", invalid)
		}
	}
	if ErrInvalidDescriptor("bad").Error() != "bad" {
		t.Fatal("descriptor error mismatch")
	}
}

func TestProcessPoolDispatchesRuntimeBufferFrames(t *testing.T) {
	if os.Getenv("OVRT_PROCESS_HELPER") == "1" {
		runProcessPoolHelper(t)
		return
	}

	pool, err := NewProcessPool(ProcessPoolOptions{
		Command: []string{os.Args[0], "-test.run=TestProcessPoolDispatchesRuntimeBufferFrames", "--"},
		Env:     []string{"OVRT_PROCESS_HELPER=1"},
		Workers: 1,
	})
	if err != nil {
		t.Fatalf("NewProcessPool() error = %v", err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	response, err := pool.Execute(context.Background(), ProcessRequest{
		UnitID:        "runtime.echo",
		Input:         []byte("stable-safe-zone"),
		ContextHash:   41,
		ModuleVersion: 7,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if string(response.Output) != "STABLE-SAFE-ZONE" {
		t.Fatalf("unexpected output payload %q", string(response.Output))
	}
	if response.OutputEpoch != 1 {
		t.Fatalf("unexpected output epoch %d", response.OutputEpoch)
	}
	if response.Diagnostics != "" {
		t.Fatalf("unexpected diagnostics %q", response.Diagnostics)
	}
}

func TestProcessPoolSupportsSharedMemoryTransport(t *testing.T) {
	if !sharedMemorySupported("") {
		t.Skip("shared memory transport is not supported on this runtime")
	}
	if os.Getenv("OVRT_PROCESS_HELPER") == "1" {
		runProcessPoolHelper(t)
		return
	}

	pool, err := NewProcessPool(ProcessPoolOptions{
		Command:   []string{os.Args[0], "-test.run=TestProcessPoolSupportsSharedMemoryTransport", "--"},
		Env:       []string{"OVRT_PROCESS_HELPER=1"},
		Workers:   1,
		Transport: ProcessTransportSharedMemory,
	})
	if err != nil {
		t.Fatalf("NewProcessPool() error = %v", err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	response, err := pool.Execute(context.Background(), ProcessRequest{
		UnitID:        "runtime.echo",
		Input:         []byte("shared-safe-zone"),
		ContextHash:   42,
		ModuleVersion: 7,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if string(response.Output) != "SHARED-SAFE-ZONE" {
		t.Fatalf("unexpected output payload %q", string(response.Output))
	}
}

func TestFFIPoolDispatchesRuntimeBufferFrames(t *testing.T) {
	libraryPath := buildFFITestLibrary(t)
	if libraryPath == "" {
		t.Skip("ffi test library could not be built")
	}

	pool, err := NewFFIPool(FFIPoolOptions{
		LibraryPath: libraryPath,
		Workers:     2,
	})
	if err != nil {
		t.Fatalf("NewFFIPool() error = %v", err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	response, err := pool.Execute(context.Background(), ProcessRequest{
		UnitID:        "runtime.echo",
		Input:         []byte("ffi-safe-zone"),
		ContextHash:   84,
		ModuleVersion: 7,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if string(response.Output) != "FFI-SAFE-ZONE" {
		t.Fatalf("unexpected output payload %q", string(response.Output))
	}
	if response.OutputEpoch != 1 {
		t.Fatalf("unexpected output epoch %d", response.OutputEpoch)
	}
}

func TestDefaultProcessWorkerCount(t *testing.T) {
	cases := map[int]int{
		1:  1,
		4:  2,
		8:  4,
		16: 8,
		32: 12,
	}
	for cores, want := range cases {
		if got := defaultProcessWorkerCount(cores); got != want {
			t.Fatalf("defaultProcessWorkerCount(%d) = %d, want %d", cores, got, want)
		}
	}
}

func TestPreferredWorkerIndexUsesContextHash(t *testing.T) {
	pool := &ProcessPool{
		allWorkers: []*processWorker{{}, {}, {}, {}},
	}
	if got := pool.preferredWorkerIndex(9); got != 1 {
		t.Fatalf("preferredWorkerIndex(9) = %d, want 1", got)
	}
	if got := pool.preferredWorkerIndex(-10); got != 2 {
		t.Fatalf("preferredWorkerIndex(-10) = %d, want 2", got)
	}
}

func TestResolveProcessTransportMode(t *testing.T) {
	mode, err := resolveProcessTransportMode(ProcessTransportAuto, "")
	if err != nil {
		t.Fatalf("resolveProcessTransportMode(auto) error = %v", err)
	}
	if mode != ProcessTransportStdio && mode != ProcessTransportSharedMemory {
		t.Fatalf("unexpected transport mode: %s", mode)
	}
	if _, err := resolveProcessTransportMode(ProcessTransportMode("invalid"), ""); err == nil {
		t.Fatal("expected invalid transport mode to fail")
	}
	if !sharedMemorySupported("") {
		if _, err := resolveProcessTransportMode(ProcessTransportSharedMemory, ""); err == nil {
			t.Fatal("expected explicit shared memory mode to fail when unsupported")
		}
	} else {
		mode, err := resolveProcessTransportMode(ProcessTransportSharedMemory, "")
		if err != nil {
			t.Fatalf("resolveProcessTransportMode(shm) error = %v", err)
		}
		if mode != ProcessTransportSharedMemory {
			t.Fatalf("unexpected shared memory mode: %s", mode)
		}
	}
	mode, err = resolveProcessTransportMode(ProcessTransportFFI, "")
	if err != nil {
		t.Fatalf("resolveProcessTransportMode(ffi) error = %v", err)
	}
	if mode != ProcessTransportFFI {
		t.Fatalf("unexpected ffi mode: %s", mode)
	}
}

func TestResolveProcessTransportSupportReportsFallbacks(t *testing.T) {
	support, err := ResolveProcessTransportSupport(ProcessTransportAuto, "")
	if err != nil {
		t.Fatalf("ResolveProcessTransportSupport(auto) error = %v", err)
	}
	if support.Requested != ProcessTransportAuto {
		t.Fatalf("Requested = %s, want auto", support.Requested)
	}
	if support.Resolved != ProcessTransportStdio && support.Resolved != ProcessTransportSharedMemory {
		t.Fatalf("Resolved = %s", support.Resolved)
	}
	if _, err := ResolveProcessTransportSupport(ProcessTransportMode("bogus"), ""); err == nil {
		t.Fatal("expected unsupported transport to fail")
	}
	if !sharedMemorySupported("") {
		if _, err := ResolveProcessTransportSupport(ProcessTransportSharedMemory, ""); err == nil {
			t.Fatal("expected unsupported shared memory transport to fail")
		}
	}
}

func TestProcessPoolDiagnosticsAndErrorPaths(t *testing.T) {
	if _, err := NewProcessPool(ProcessPoolOptions{}); err == nil {
		t.Fatal("expected missing command to fail")
	}
	if _, err := (*ProcessPool)(nil).Execute(context.Background(), ProcessRequest{UnitID: "unit"}); err == nil {
		t.Fatal("expected nil pool execute to fail")
	}
	pool := &ProcessPool{
		exchangeTimeout: DefaultProcessExchangeTimeout,
		bufferPool: sync.Pool{New: func() any {
			return make([]byte, generated.BUFFER_TOTAL_BYTES)
		}},
	}
	if _, err := pool.Execute(context.Background(), ProcessRequest{}); err == nil {
		t.Fatal("expected missing unit id to fail")
	}
	if _, err := pool.Execute(context.Background(), ProcessRequest{UnitID: "unit"}); err == nil {
		t.Fatal("expected no workers to fail")
	}
	if got := (*ProcessPool)(nil).Diagnostics(); got.ExchangeTimeoutMS != 0 || len(got.Workers) != 0 {
		t.Fatalf("nil diagnostics = %+v", got)
	}
	worker := &processWorker{index: 1, mode: ProcessTransportStdio}
	worker.busy.Store(true)
	worker.recordStarted()
	worker.recordFailure(fmt.Errorf("boom"))
	worker.incrementRestart()
	snapshot := worker.snapshot()
	if !snapshot.Busy || snapshot.LastError != "boom" || snapshot.RestartCount != 1 || snapshot.LastStarted.IsZero() || snapshot.LastFailure.IsZero() {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	worker.recordSuccess()
	if worker.snapshot().LastError != "" || worker.snapshot().LastSuccess.IsZero() {
		t.Fatalf("success snapshot = %+v", worker.snapshot())
	}
	pool = &ProcessPool{
		exchangeTimeout: 2 * time.Second,
		transport:       ProcessTransportSupport{Requested: ProcessTransportStdio, Resolved: ProcessTransportStdio},
		allWorkers:      []*processWorker{worker},
	}
	diagnostics := pool.Diagnostics()
	if diagnostics.ExchangeTimeoutMS != 2000 || len(diagnostics.Workers) != 1 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestFrameHelpersAndStringTrim(t *testing.T) {
	var frame bytes.Buffer
	if err := writeFrame(&frame, []byte("payload")); err != nil {
		t.Fatalf("writeFrame() error = %v", err)
	}
	payload, err := readFrame(&frame)
	if err != nil || string(payload) != "payload" {
		t.Fatalf("readFrame() = %q err=%v", string(payload), err)
	}
	if _, err := readFrame(strings.NewReader("\x05\x00")); err == nil {
		t.Fatal("expected short frame to fail")
	}
	frame.Reset()
	if err := writeStringFrame(&frame, "runtime.echo"); err != nil {
		t.Fatalf("writeStringFrame() error = %v", err)
	}
	payload, err = readFrame(&frame)
	if err != nil || string(payload) != "runtime.echo" {
		t.Fatalf("readFrame(string) = %q err=%v", string(payload), err)
	}
	frame.Reset()
	if err := writeFrame(&frame, []byte("abcd")); err != nil {
		t.Fatalf("writeFrame() error = %v", err)
	}
	var dst [4]byte
	if err := readFrameInto(&frame, dst[:]); err != nil {
		t.Fatalf("readFrameInto() error = %v", err)
	}
	if string(dst[:]) != "abcd" {
		t.Fatalf("readFrameInto dst = %q, want abcd", string(dst[:]))
	}
	frame.Reset()
	if err := writeFrame(&frame, []byte("toolong")); err != nil {
		t.Fatalf("writeFrame() error = %v", err)
	}
	if err := readFrameInto(&frame, dst[:]); err == nil {
		t.Fatal("expected readFrameInto length mismatch")
	}
	if stringsTrim(" \n\tvalue\r ") != "value" {
		t.Fatal("stringsTrim failed")
	}
	if cStringBytes([]byte{'o', 'k', 0, 'x'}) != "ok" || cStringBytes([]byte{'o', 'k'}) != "ok" {
		t.Fatal("cStringBytes failed")
	}
}

func runProcessPoolHelper(t *testing.T) {
	t.Helper()
	if os.Getenv("OVRT_RUNTIME_TRANSPORT") == string(ProcessTransportSharedMemory) {
		runSharedMemoryProcessPoolHelper(t)
		return
	}

	for {
		unitID, err := readFrame(os.Stdin)
		if err != nil {
			return
		}
		raw, err := readFrame(os.Stdin)
		if err != nil {
			t.Fatalf("read buffer frame: %v", err)
		}
		buffer, err := NewBuffer(raw)
		if err != nil {
			t.Fatalf("NewBuffer() error = %v", err)
		}
		input, err := buffer.InputBytes()
		if err != nil {
			t.Fatalf("InputBytes() error = %v", err)
		}
		if err := buffer.SetHeaderInt(generated.INT_IDX_STATUS_CODE, 0); err != nil {
			t.Fatalf("SetHeaderInt() error = %v", err)
		}
		if err := buffer.SetOutputBytes([]byte(strings.ToUpper(string(input)))); err != nil {
			t.Fatalf("SetOutputBytes() error = %v", err)
		}
		if _, err := buffer.AddEpoch(generated.IDX_OUTPUT_WRITTEN, 1); err != nil {
			t.Fatalf("AddEpoch() error = %v", err)
		}
		if string(unitID) == "runtime.fail" {
			if err := buffer.SetDiagnosticsText("forced failure"); err != nil {
				t.Fatalf("SetDiagnosticsText() error = %v", err)
			}
			if err := buffer.SetHeaderInt(generated.INT_IDX_STATUS_CODE, 1); err != nil {
				t.Fatalf("SetHeaderInt() error = %v", err)
			}
		}
		if err := writeFrame(os.Stdout, raw); err != nil {
			t.Fatalf("writeFrame() error = %v", err)
		}
	}
}

func runSharedMemoryProcessPoolHelper(t *testing.T) {
	t.Helper()

	path := os.Getenv("OVRT_SHM_PATH")
	if strings.TrimSpace(path) == "" {
		t.Fatal("OVRT_SHM_PATH is required for shared memory helper")
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer func() {
		_ = file.Close()
	}()

	for {
		unitID, err := readFrame(os.Stdin)
		if err != nil {
			return
		}
		raw := make([]byte, generated.BUFFER_TOTAL_BYTES)
		if _, err := file.ReadAt(raw, 0); err != nil {
			t.Fatalf("ReadAt() error = %v", err)
		}
		buffer, err := NewBuffer(raw)
		if err != nil {
			t.Fatalf("NewBuffer() error = %v", err)
		}
		input, err := buffer.InputBytes()
		if err != nil {
			t.Fatalf("InputBytes() error = %v", err)
		}
		if err := buffer.SetHeaderInt(generated.INT_IDX_STATUS_CODE, 0); err != nil {
			t.Fatalf("SetHeaderInt() error = %v", err)
		}
		if err := buffer.SetOutputBytes([]byte(strings.ToUpper(string(input)))); err != nil {
			t.Fatalf("SetOutputBytes() error = %v", err)
		}
		if _, err := buffer.AddEpoch(generated.IDX_OUTPUT_WRITTEN, 1); err != nil {
			t.Fatalf("AddEpoch() error = %v", err)
		}
		if string(unitID) == "runtime.fail" {
			if err := buffer.SetDiagnosticsText("forced failure"); err != nil {
				t.Fatalf("SetDiagnosticsText() error = %v", err)
			}
			if err := buffer.SetHeaderInt(generated.INT_IDX_STATUS_CODE, 1); err != nil {
				t.Fatalf("SetHeaderInt() error = %v", err)
			}
		}
		if _, err := file.WriteAt(raw, 0); err != nil {
			t.Fatalf("WriteAt() error = %v", err)
		}
		if err := writeFrame(os.Stdout, nil); err != nil {
			t.Fatalf("writeFrame() error = %v", err)
		}
	}
}

func buildFFITestLibrary(t *testing.T) string {
	t.Helper()

	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc compiler not available")
	}

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "runtime_ffi_stub.c")
	libraryPath := filepath.Join(dir, ffiLibraryFileName())
	source := fmt.Sprintf(`#include <ctype.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

enum {
  BUFFER_TOTAL_BYTES = %d,
  INPUT_LENGTH_OFFSET = %d,
  OUTPUT_LENGTH_OFFSET = %d,
  STATUS_CODE_OFFSET = %d,
  OUTPUT_WRITTEN_EPOCH_OFFSET = %d,
  INPUT_BYTES_OFFSET = %d,
  OUTPUT_BYTES_OFFSET = %d,
  DIAGNOSTIC_BYTES_OFFSET = %d,
  DIAGNOSTIC_MAX_BYTES = %d
};

static int32_t read_i32(const uint8_t* raw, uintptr_t offset) {
  int32_t value = 0;
  memcpy(&value, raw + offset, sizeof(value));
  return value;
}

static void write_i32(uint8_t* raw, uintptr_t offset, int32_t value) {
  memcpy(raw + offset, &value, sizeof(value));
}

uint32_t ovrt_runtime_abi_version(void) {
  return 1;
}

int32_t ovrt_runtime_create(uintptr_t workers, void** out_host, char* err_buf, uintptr_t err_cap) {
  (void)workers;
  (void)err_buf;
  (void)err_cap;
  if (out_host == NULL) {
    return 1;
  }
  *out_host = (void*)0x1;
  return 0;
}

void ovrt_runtime_destroy(void* host) {
  (void)host;
}

int32_t ovrt_runtime_process_buffer(void* host, const uint8_t* unit_id, uintptr_t unit_id_len, uint8_t* buffer, uintptr_t buffer_len, char* err_buf, uintptr_t err_cap) {
  (void)host;
  (void)unit_id;
  (void)unit_id_len;
  if (buffer == NULL || buffer_len != BUFFER_TOTAL_BYTES) {
    if (err_buf != NULL && err_cap > 0) {
      snprintf(err_buf, err_cap, "invalid runtime buffer");
    }
    return 1;
  }
  int32_t input_len = read_i32(buffer, INPUT_LENGTH_OFFSET);
  if (input_len < 0) {
    if (err_buf != NULL && err_cap > 0) {
      snprintf(err_buf, err_cap, "invalid input length");
    }
    return 1;
  }
  memset(buffer + OUTPUT_BYTES_OFFSET, 0, BUFFER_TOTAL_BYTES - OUTPUT_BYTES_OFFSET);
  for (int32_t index = 0; index < input_len; index++) {
    buffer[OUTPUT_BYTES_OFFSET + index] = (uint8_t)toupper((unsigned char)buffer[INPUT_BYTES_OFFSET + index]);
  }
  write_i32(buffer, OUTPUT_LENGTH_OFFSET, input_len);
  write_i32(buffer, STATUS_CODE_OFFSET, 0);
  memset(buffer + DIAGNOSTIC_BYTES_OFFSET, 0, DIAGNOSTIC_MAX_BYTES);
  write_i32(buffer, OUTPUT_WRITTEN_EPOCH_OFFSET, read_i32(buffer, OUTPUT_WRITTEN_EPOCH_OFFSET) + 1);
  return 0;
}
`,
		generated.BUFFER_TOTAL_BYTES,
		generated.OFFSET_HEADER_INTS+generated.INT_IDX_INPUT_LENGTH*4,
		generated.OFFSET_HEADER_INTS+generated.INT_IDX_OUTPUT_LENGTH*4,
		generated.OFFSET_HEADER_INTS+generated.INT_IDX_STATUS_CODE*4,
		generated.OFFSET_EPOCHS+generated.IDX_OUTPUT_WRITTEN*generated.EPOCH_SLOT_BYTES,
		generated.OFFSET_INPUT_BYTES,
		generated.OFFSET_OUTPUT_BYTES,
		generated.OFFSET_DIAGNOSTIC_BYTES,
		generated.DIAGNOSTIC_MAX_BYTES,
	)
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	args := []string{"-O2", "-o", libraryPath}
	switch runtime.GOOS {
	case "darwin":
		args = append(args, "-dynamiclib", sourcePath)
	default:
		args = append(args, "-shared", "-fPIC", sourcePath)
	}
	cmd := exec.Command(cc, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile ffi test library: %v\n%s", err, string(output))
	}
	return libraryPath
}

func ffiLibraryFileName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libruntime_ffi_stub.dylib"
	default:
		return "libruntime_ffi_stub.so"
	}
}
