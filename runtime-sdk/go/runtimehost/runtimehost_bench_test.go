package runtimehost

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := buffer.DiagnosticsText(); got == "" {
			b.Fatal("empty diagnostics")
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
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
