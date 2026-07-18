package runtimehost

import (
	"bytes"
	"context"
	"encoding/binary"
	"sync"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

type benchmarkProcessExchange struct{}

var benchmarkProcessOutput [1024]byte

func (benchmarkProcessExchange) Exchange(_ context.Context, _ string, raw []byte) error {
	buffer, err := NewBuffer(raw)
	if err != nil {
		return err
	}
	if err := buffer.SetOutputBytesFast(benchmarkProcessOutput[:]); err != nil {
		return err
	}
	_, err = buffer.AddEpoch(generated.IDX_OUTPUT_WRITTEN, 1)
	return err
}

func (benchmarkProcessExchange) Close() error   { return nil }
func (benchmarkProcessExchange) Restart() error { return nil }

func benchmarkBuffer(b *testing.B) *Buffer {
	b.Helper()
	buffer, err := NewBuffer(make([]byte, generated.BUFFER_TOTAL_BYTES))
	if err != nil {
		b.Fatalf("NewBuffer() error = %v", err)
	}
	buffer.Initialize(1)
	return buffer
}

func BenchmarkBufferSetInputBytes1KB(b *testing.B) {
	buffer := benchmarkBuffer(b)
	payload := bytes.Repeat([]byte{17}, int(generated.INPUT_MAX_BYTES))
	b.ReportAllocs()
	
	for b.Loop() {
		if err := buffer.SetInputBytes(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBufferSetInputBytesFast1KB(b *testing.B) {
	buffer := benchmarkBuffer(b)
	payload := bytes.Repeat([]byte{17}, int(generated.INPUT_MAX_BYTES))
	buffer.Reset()
	buffer.Initialize(1)
	b.ReportAllocs()
	
	for b.Loop() {
		if err := buffer.SetInputBytesFast(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBufferInputBytesOwned1KB(b *testing.B) {
	buffer := benchmarkBuffer(b)
	payload := bytes.Repeat([]byte{17}, int(generated.INPUT_MAX_BYTES))
	if err := buffer.SetInputBytes(payload); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	
	for b.Loop() {
		got, err := buffer.InputBytes()
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != len(payload) {
			b.Fatalf("input length = %d, want %d", len(got), len(payload))
		}
	}
}

func BenchmarkBufferInputBytesView1KB(b *testing.B) {
	buffer := benchmarkBuffer(b)
	payload := bytes.Repeat([]byte{17}, int(generated.INPUT_MAX_BYTES))
	if err := buffer.SetInputBytes(payload); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	
	for b.Loop() {
		got, err := buffer.InputBytesView()
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != len(payload) {
			b.Fatalf("input length = %d, want %d", len(got), len(payload))
		}
	}
}

func BenchmarkBufferSetOutputBytes2KB(b *testing.B) {
	buffer := benchmarkBuffer(b)
	payload := bytes.Repeat([]byte{29}, int(generated.OUTPUT_MAX_BYTES))
	b.ReportAllocs()
	
	for b.Loop() {
		if err := buffer.SetOutputBytes(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBufferSetOutputBytesFast2KB(b *testing.B) {
	buffer := benchmarkBuffer(b)
	payload := bytes.Repeat([]byte{29}, int(generated.OUTPUT_MAX_BYTES))
	buffer.Reset()
	buffer.Initialize(1)
	b.ReportAllocs()
	
	for b.Loop() {
		if err := buffer.SetOutputBytesFast(payload); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBufferOutputBytesOwned2KB(b *testing.B) {
	buffer := benchmarkBuffer(b)
	payload := bytes.Repeat([]byte{29}, int(generated.OUTPUT_MAX_BYTES))
	if err := buffer.SetOutputBytes(payload); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	
	for b.Loop() {
		got, err := buffer.OutputBytes()
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != len(payload) {
			b.Fatalf("output length = %d, want %d", len(got), len(payload))
		}
	}
}

func BenchmarkBufferOutputBytesView2KB(b *testing.B) {
	buffer := benchmarkBuffer(b)
	payload := bytes.Repeat([]byte{29}, int(generated.OUTPUT_MAX_BYTES))
	if err := buffer.SetOutputBytes(payload); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	
	for b.Loop() {
		got, err := buffer.OutputBytesView()
		if err != nil {
			b.Fatal(err)
		}
		if len(got) != len(payload) {
			b.Fatalf("output length = %d, want %d", len(got), len(payload))
		}
	}
}

func BenchmarkBufferEpochAdd(b *testing.B) {
	buffer := benchmarkBuffer(b)
	b.ReportAllocs()
	
	for b.Loop() {
		if _, err := buffer.AddEpoch(generated.IDX_RUNTIME_TICK, 1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBufferDiagnosticsText(b *testing.B) {
	buffer := benchmarkBuffer(b)
	if err := buffer.SetDiagnosticsText("runtime diagnostics are bounded and inspectable"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	
	for b.Loop() {
		if got := buffer.DiagnosticsText(); got == "" {
			b.Fatal("empty diagnostics")
		}
	}
}

func benchmarkProcessPool() *ProcessPool {
	return &ProcessPool{
		exchangeTimeout: DefaultProcessExchangeTimeout,
		allWorkers: []*processWorker{{
			testExchange: benchmarkProcessExchange{},
		}},
		bufferPool: sync.Pool{New: func() any {
			buffer := make([]byte, generated.BUFFER_TOTAL_BYTES)
			return &buffer
		}},
	}
}

func BenchmarkProcessPoolExecuteOwned1KB(b *testing.B) {
	pool := benchmarkProcessPool()
	request := ProcessRequest{UnitID: "runtime.bench", Input: []byte("input")}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response, err := pool.Execute(context.Background(), request)
		if err != nil || len(response.Output) != 1024 {
			b.Fatalf("Execute() output=%d err=%v", len(response.Output), err)
		}
	}
}

func BenchmarkProcessPoolExecuteInto1KB(b *testing.B) {
	pool := benchmarkProcessPool()
	request := ProcessRequest{UnitID: "runtime.bench", Input: []byte("input")}
	dst := make([]byte, generated.OUTPUT_MAX_BYTES)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		response, err := pool.ExecuteInto(context.Background(), request, dst)
		if err != nil || len(response.Output) != 1024 {
			b.Fatalf("ExecuteInto() output=%d err=%v", len(response.Output), err)
		}
	}
}

func BenchmarkRuntimeNativeGPUDescriptorValidate(b *testing.B) {
	descriptor := RuntimeNativeGPUDescriptor{
		ID:         "camera.frame.42",
		Kind:       RuntimeNativeGPUKindTexture,
		Platform:   RuntimeNativeGPUPlatformAppleIOSurface,
		Width:      1920,
		Height:     1080,
		Format:     "bgra8",
		SchemaName: "media/v1/frame.capnp",
		Producer:   "camera.plugin",
		Fallback:   RuntimeNativeGPUFallbackCopyToWebGPU,
	}
	b.ReportAllocs()
	
	for b.Loop() {
		if err := descriptor.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBufferReadFrameAllocCopy4KB(b *testing.B) {
	frame := makeRuntimeBufferFrame()
	dst := make([]byte, generated.BUFFER_TOTAL_BYTES)
	reader := bytes.NewReader(frame)
	b.ReportAllocs()
	
	for b.Loop() {
		reader.Reset(frame)
		updated, err := readFrame(reader)
		if err != nil {
			b.Fatal(err)
		}
		copy(dst, updated)
	}
}

func BenchmarkBufferReadFrameInto4KB(b *testing.B) {
	frame := makeRuntimeBufferFrame()
	dst := make([]byte, generated.BUFFER_TOTAL_BYTES)
	reader := bytes.NewReader(frame)
	b.ReportAllocs()
	
	for b.Loop() {
		reader.Reset(frame)
		if err := readFrameInto(reader, dst); err != nil {
			b.Fatal(err)
		}
	}
}

func makeRuntimeBufferFrame() []byte {
	frame := make([]byte, 4+generated.BUFFER_TOTAL_BYTES)
	binary.LittleEndian.PutUint32(frame[:4], generated.BUFFER_TOTAL_BYTES)
	for index := 4; index < len(frame); index++ {
		frame[index] = byte(index)
	}
	return frame
}
