package runtimehost

import (
	"bytes"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

func benchmarkBuffer(b *testing.B) *Buffer {
	b.Helper()
	buffer, err := NewBuffer(make([]byte, generated.BUFFER_TOTAL_BYTES))
	if err != nil {
		b.Fatalf("NewBuffer() error = %v", err)
	}
	if err := buffer.Initialize(1); err != nil {
		b.Fatalf("Initialize() error = %v", err)
	}
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
