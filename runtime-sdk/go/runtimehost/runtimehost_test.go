package runtimehost

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
)

func TestBufferRoundTrip(t *testing.T) {
	raw := make([]byte, generated.BUFFER_TOTAL_BYTES)
	buffer, err := NewBuffer(raw)
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}

	buffer.Initialize(4)
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
	buffer.Initialize(1)

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

func TestProcessPoolReportsRuntimeStatusFailure(t *testing.T) {
	if os.Getenv("OVRT_PROCESS_HELPER") == "1" {
		runProcessPoolHelper(t)
		return
	}

	pool, err := NewProcessPool(ProcessPoolOptions{
		Command: []string{os.Args[0], "-test.run=TestProcessPoolReportsRuntimeStatusFailure", "--"},
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
		UnitID:        "runtime.fail",
		Input:         []byte("bad"),
		ModuleVersion: 1,
	})
	if err == nil || response.StatusCode != 1 || response.Diagnostics != "forced failure" {
		t.Fatalf("Execute(runtime.fail) response=%+v err=%v", response, err)
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
	failed, err := pool.Execute(context.Background(), ProcessRequest{
		UnitID:        "runtime.fail",
		Input:         []byte("ffi-fail"),
		ModuleVersion: 7,
	})
	if err == nil || failed.StatusCode != 1 || failed.Diagnostics != "forced ffi failure" {
		t.Fatalf("ffi failure response=%+v err=%v", failed, err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := pool.Execute(context.Background(), ProcessRequest{UnitID: "runtime.echo"}); err == nil {
		t.Fatal("expected closed ffi pool execute to fail")
	}
}

func TestFFIPoolErrorBoundaries(t *testing.T) {
	if _, err := NewFFIPool(FFIPoolOptions{}); err == nil {
		t.Fatal("expected missing ffi library path to fail")
	}
	if _, err := NewFFIPool(FFIPoolOptions{LibraryPath: filepath.Join(t.TempDir(), "missing.so")}); err == nil {
		t.Fatal("expected missing ffi library to fail")
	}
	if _, err := (*FFIPool)(nil).Execute(context.Background(), ProcessRequest{UnitID: "unit"}); err == nil {
		t.Fatal("expected nil ffi pool execute to fail")
	}
	pool := &FFIPool{}
	if _, err := pool.Execute(context.Background(), ProcessRequest{}); err == nil {
		t.Fatal("expected missing unit id to fail")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pool.Execute(cancelled, ProcessRequest{UnitID: "unit"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ffi Execute() error = %v", err)
	}
	pool.bufferPool = sync.Pool{New: func() any {
		buffer := make([]byte, generated.BUFFER_TOTAL_BYTES)
		return &buffer
	}}
	if _, err := pool.Execute(context.Background(), ProcessRequest{UnitID: "unit"}); err == nil {
		t.Fatal("expected closed ffi host to fail")
	}
	if err := (*FFIPool)(nil).Close(); err != nil {
		t.Fatalf("nil Close() error = %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("empty Close() error = %v", err)
	}

	for _, tt := range []struct {
		name   string
		source string
	}{
		{"missing process symbol", ffiMinimalSource(1, "ok", false)},
		{"abi mismatch", ffiMinimalSource(2, "ok", true)},
		{"create failure", ffiMinimalSource(1, "fail", true)},
		{"nil host", ffiMinimalSource(1, "nil", true)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewFFIPool(FFIPoolOptions{LibraryPath: buildFFITestLibrarySource(t, tt.source)}); err == nil {
				t.Fatal("expected ffi setup to fail")
			}
		})
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
	if normalizeProcessTransportMode(ProcessTransportMode(" STDIO ")) != ProcessTransportStdio {
		t.Fatal("normalizeProcessTransportMode did not normalize stdio")
	}
}

func TestResolveProcessTransportSupportReportsFallbacks(t *testing.T) {
	supported := SupportedProcessTransports("")
	if len(supported) == 0 || supported[0] != ProcessTransportStdio {
		t.Fatalf("supported transports = %+v", supported)
	}
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
	if _, err := NewProcessPool(ProcessPoolOptions{Command: []string{filepath.Join(t.TempDir(), "missing-runtime")}}); err == nil {
		t.Fatal("expected missing runtime command to fail")
	}
	if _, err := (*ProcessPool)(nil).Execute(context.Background(), ProcessRequest{UnitID: "unit"}); err == nil {
		t.Fatal("expected nil pool execute to fail")
	}
	pool := &ProcessPool{
		exchangeTimeout: DefaultProcessExchangeTimeout,
		bufferPool: sync.Pool{New: func() any {
			buffer := make([]byte, generated.BUFFER_TOTAL_BYTES)
			return &buffer
		}},
	}
	if _, err := pool.Execute(context.Background(), ProcessRequest{}); err == nil {
		t.Fatal("expected missing unit id to fail")
	}
	if _, err := pool.Execute(context.Background(), ProcessRequest{UnitID: "unit", Input: make([]byte, generated.INPUT_MAX_BYTES+1)}); err == nil {
		t.Fatal("expected oversized process input to fail")
	}
	if _, err := pool.Execute(context.Background(), ProcessRequest{UnitID: "unit"}); err == nil {
		t.Fatal("expected no workers to fail")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	pool.allWorkers = []*processWorker{{}}
	if _, err := pool.Execute(cancelled, ProcessRequest{UnitID: "unit"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Execute() error = %v", err)
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

func TestProcessPoolWorkerSelectionBusyAndFallback(t *testing.T) {
	pool := &ProcessPool{
		allWorkers: []*processWorker{{}, {}},
	}
	pool.allWorkers[0].mu.Lock()
	defer pool.allWorkers[0].mu.Unlock()
	pool.allWorkers[1] = workerWithEchoTransport(t, ProcessTransportStdio)
	if err := pool.executeOnSelectedWorker(context.Background(), ProcessRequest{UnitID: "runtime.echo"}, newRuntimeBuffer(t, "busy")); err != nil {
		t.Fatalf("executeOnSelectedWorker() error = %v", err)
	}
	worker := workerWithEchoTransport(t, ProcessTransportStdio)
	buffer := newRuntimeBuffer(t, "direct")
	if err := worker.execute(context.Background(), "runtime.echo", buffer); err != nil {
		t.Fatalf("worker.execute() error = %v", err)
	}
	parsed, err := NewBuffer(buffer)
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}
	output, err := parsed.OutputBytes()
	if err != nil || string(output) != "DIRECT" {
		t.Fatalf("direct worker output = %q err=%v", string(output), err)
	}
	plain := stdioExchange{stdin: nopWriteCloser{Writer: io.Discard}}
	if err := plain.Close(); err != nil {
		t.Fatalf("stdio Close() error = %v", err)
	}
	if err := plain.Restart(); err != nil {
		t.Fatalf("stdio Restart() error = %v", err)
	}
}

func TestProcessWorkerExchangeRestartAndClosePaths(t *testing.T) {
	exchange := &scriptedExchange{
		errs: []error{errors.New("first exchange failed"), nil},
	}
	worker := &processWorker{
		logger:       testLogger(t),
		mode:         ProcessTransportStdio,
		testExchange: exchange,
	}
	if err := worker.execute(context.Background(), "runtime.echo", newRuntimeBuffer(t, "restart")); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if exchange.calls != 2 || exchange.restarts != 1 || worker.snapshot().RestartCount != 1 {
		t.Fatalf("exchange=%+v snapshot=%+v", exchange, worker.snapshot())
	}
	if err := worker.close(); err != nil || exchange.closes != 1 {
		t.Fatalf("close err=%v exchange=%+v", err, exchange)
	}

	restartFailure := &scriptedExchange{
		errs:       []error{errors.New("exchange failed")},
		restartErr: errors.New("restart failed"),
	}
	worker = &processWorker{
		logger:       testLogger(t),
		testExchange: restartFailure,
	}
	if err := worker.execute(context.Background(), "runtime.echo", newRuntimeBuffer(t, "restart-fail")); err == nil || !strings.Contains(err.Error(), "restart native runtime worker") {
		t.Fatalf("restart failure error = %v", err)
	}

	cancelled := &scriptedExchange{waitForContext: true}
	worker = &processWorker{
		logger:       testLogger(t),
		testExchange: cancelled,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.execute(ctx, "runtime.echo", newRuntimeBuffer(t, "cancel")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled execute error = %v", err)
	}

	timeoutExchange := &scriptedExchange{waitForContext: true}
	worker = &processWorker{
		logger:       testLogger(t),
		testExchange: timeoutExchange,
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer timeoutCancel()
	if err := worker.executeWithContext(timeoutCtx, "runtime.echo", newRuntimeBuffer(t, "timeout")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout exchange error = %v", err)
	}
}

func TestProcessWorkerSharedMemoryGuard(t *testing.T) {
	worker := &processWorker{mode: ProcessTransportSharedMemory, stdin: nopWriteCloser{Writer: io.Discard}, stdout: bufio.NewReader(strings.NewReader(""))}
	if err := worker.executeLocked("runtime.echo", newRuntimeBuffer(t, "shm")); err == nil {
		t.Fatal("expected shared memory execution without segment to fail")
	}
	if _, err := newSharedMemorySegment(""); sharedMemorySupported("") || err == nil {
		t.Fatalf("unsupported shared memory probe supported=%v err=%v", sharedMemorySupported(""), err)
	}
	if err := (*sharedMemorySegment)(nil).Close(); err != nil {
		t.Fatalf("nil shared memory Close() error = %v", err)
	}
	var ack bytes.Buffer
	if err := writeFrame(&ack, nil); err != nil {
		t.Fatalf("write ack error = %v", err)
	}
	buffer := newRuntimeBuffer(t, "shared")
	worker = &processWorker{
		shm:    &sharedMemorySegment{raw: make([]byte, generated.BUFFER_TOTAL_BYTES)},
		stdin:  nopWriteCloser{Writer: io.Discard},
		stdout: bufio.NewReader(&ack),
	}
	if err := worker.executeSharedMemoryLocked("runtime.echo", buffer); err != nil {
		t.Fatalf("executeSharedMemoryLocked() error = %v", err)
	}
	var badAck bytes.Buffer
	if err := writeFrame(&badAck, []byte("bad")); err != nil {
		t.Fatalf("write bad ack error = %v", err)
	}
	worker.stdout = bufio.NewReader(&badAck)
	if err := worker.executeSharedMemoryLocked("runtime.echo", buffer); err == nil {
		t.Fatal("expected bad shared memory ack to fail")
	}
}

func TestFrameHelpersAndStringTrim(t *testing.T) {
	var frame bytes.Buffer
	if err := writeFrame(&frame, []byte("payload")); err != nil {
		t.Fatalf("writeFrame() error = %v", err)
	}
	if err := writeFrame(errorWriter{}, []byte("payload")); err == nil {
		t.Fatal("expected writeFrame writer failure")
	}
	if err := writeStringFrame(errorWriter{}, "payload"); err == nil {
		t.Fatal("expected writeStringFrame writer failure")
	}
	payload, err := readFrame(&frame)
	if err != nil || string(payload) != "payload" {
		t.Fatalf("readFrame() = %q err=%v", string(payload), err)
	}
	if _, err := readFrame(strings.NewReader("\x05\x00")); err == nil {
		t.Fatal("expected short frame to fail")
	}
	if _, err := checkedFrameSize(-1); err == nil {
		t.Fatal("expected negative frame size to fail")
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
	logWorker := &processWorker{logger: testLogger(t)}
	logWorker.logStderr(scanErrorReadCloser{})
}

func workerWithEchoTransport(t *testing.T, mode ProcessTransportMode) *processWorker {
	t.Helper()
	serverToClientReader, serverToClientWriter := io.Pipe()
	clientToServerReader, clientToServerWriter := io.Pipe()
	worker := &processWorker{
		stdin:  clientToServerWriter,
		stdout: bufio.NewReader(serverToClientReader),
		logger: testLogger(t),
		mode:   mode,
		cmd:    &exec.Cmd{},
	}
	t.Cleanup(func() {
		_ = serverToClientReader.Close()
		_ = serverToClientWriter.Close()
		_ = clientToServerReader.Close()
		_ = clientToServerWriter.Close()
	})
	go echoRuntimeFrames(clientToServerReader, serverToClientWriter)
	return worker
}

func echoRuntimeFrames(input io.Reader, output io.Writer) {
	for {
		unitID, err := readFrame(input)
		if err != nil {
			return
		}
		raw, err := readFrame(input)
		if err != nil {
			return
		}
		buffer, err := NewBuffer(raw)
		if err != nil {
			return
		}
		payload, err := buffer.InputBytes()
		if err != nil {
			return
		}
		_ = buffer.SetOutputBytes([]byte(strings.ToUpper(string(payload))))
		_, _ = buffer.AddEpoch(generated.IDX_OUTPUT_WRITTEN, 1)
		if string(unitID) == "runtime.fail" {
			_ = buffer.SetHeaderInt(generated.INT_IDX_STATUS_CODE, 1)
			_ = buffer.SetDiagnosticsText("forced failure")
		}
		if err := writeFrame(output, raw); err != nil {
			return
		}
	}
}

func newRuntimeBuffer(t *testing.T, payload string) []byte {
	t.Helper()
	raw := make([]byte, generated.BUFFER_TOTAL_BYTES)
	buffer, err := NewBuffer(raw)
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}
	buffer.Initialize(1)
	if err := buffer.SetInputBytesFast([]byte(payload)); err != nil {
		t.Fatalf("SetInputBytesFast() error = %v", err)
	}
	return raw
}

func testLogger(t *testing.T) logger.Logger {
	t.Helper()
	log, err := logger.NewDefault()
	if err != nil {
		t.Fatalf("NewDefault logger error = %v", err)
	}
	return log
}

type nopWriteCloser struct {
	io.Writer
}

func (w nopWriteCloser) Close() error {
	return nil
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

type scanErrorReadCloser struct{}

func (scanErrorReadCloser) Read([]byte) (int, error) {
	return 0, errors.New("scan failed")
}

func (scanErrorReadCloser) Close() error {
	return nil
}

type scriptedExchange struct {
	errs           []error
	restartErr     error
	closeErr       error
	waitForContext bool
	calls          int
	restarts       int
	closes         int
}

func (x *scriptedExchange) Exchange(ctx context.Context, _ string, _ []byte) error {
	x.calls++
	if x.waitForContext {
		<-ctx.Done()
		return ctx.Err()
	}
	if len(x.errs) == 0 {
		return nil
	}
	err := x.errs[0]
	x.errs = x.errs[1:]
	return err
}

func (x *scriptedExchange) Restart() error {
	x.restarts++
	return x.restartErr
}

func (x *scriptedExchange) Close() error {
	x.closes++
	return x.closeErr
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

	return buildFFITestLibrarySource(t, fmt.Sprintf(`#include <ctype.h>
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
  if (buffer == NULL || buffer_len != BUFFER_TOTAL_BYTES) {
    if (err_buf != NULL && err_cap > 0) {
      snprintf(err_buf, err_cap, "invalid runtime buffer");
    }
    return 1;
  }
  if (unit_id_len == 12 && memcmp(unit_id, "runtime.fail", 12) == 0) {
    write_i32(buffer, STATUS_CODE_OFFSET, 1);
    const char* message = "forced ffi failure";
    uintptr_t n = strlen(message);
    if (n > DIAGNOSTIC_MAX_BYTES) {
      n = DIAGNOSTIC_MAX_BYTES;
    }
    memset(buffer + DIAGNOSTIC_BYTES_OFFSET, 0, DIAGNOSTIC_MAX_BYTES);
    memcpy(buffer + DIAGNOSTIC_BYTES_OFFSET, message, n);
    return 0;
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
	))
}

func buildFFITestLibrarySource(t *testing.T, source string) string {
	t.Helper()

	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc compiler not available")
	}

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "runtime_ffi_stub.c")
	libraryPath := filepath.Join(dir, ffiLibraryFileName())
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

func ffiMinimalSource(abi int, createMode string, includeProcess bool) string {
	process := ""
	if includeProcess {
		process = `int32_t ovrt_runtime_process_buffer(void* host, const uint8_t* unit_id, uintptr_t unit_id_len, uint8_t* buffer, uintptr_t buffer_len, char* err_buf, uintptr_t err_cap) {
  (void)host; (void)unit_id; (void)unit_id_len; (void)buffer; (void)buffer_len; (void)err_buf; (void)err_cap;
  return 0;
}`
	}
	createBody := `*out_host = (void*)0x1; return 0;`
	switch createMode {
	case "fail":
		createBody = `snprintf(err_buf, err_cap, "create failed"); return 1;`
	case "nil":
		createBody = `*out_host = NULL; return 0;`
	}
	return fmt.Sprintf(`#include <stdint.h>
#include <stdio.h>
uint32_t ovrt_runtime_abi_version(void) { return %d; }
int32_t ovrt_runtime_create(uintptr_t workers, void** out_host, char* err_buf, uintptr_t err_cap) {
  (void)workers;
  %s
}
void ovrt_runtime_destroy(void* host) { (void)host; }
%s
`, abi, createBody, process)
}

func ffiLibraryFileName() string {
	switch runtime.GOOS {
	case "darwin":
		return "libruntime_ffi_stub.dylib"
	default:
		return "libruntime_ffi_stub.so"
	}
}
