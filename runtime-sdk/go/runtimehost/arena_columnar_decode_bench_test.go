package runtimehost

import (
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// Cost benchmarks for the float32 column read path.
//
// Three claims are measured, each of which the implementation depends on:
//
//  1. Descriptor validation is O(1) in the row count. ReadColumnarBatch checks
//     magic, bounds the column count, and compares one slab length; none of
//     that scales with rows. A regression here would mean an untrusted-input
//     check had become proportional to payload size.
//  2. The copying read costs one allocation of the whole column.
//  3. SumFloat32Column reduces without that allocation and without the
//     single-accumulator dependency chain.
//
// The size series (TE-18) exists to expose accidental super-linear work: a
// per-row cost that climbs across 1K, 64K and 1M is the signal.

func benchArena(b *testing.B, bytes uint32) *Arena {
	b.Helper()
	arena, err := NewArenaOver(make([]byte, bytes))
	if err != nil {
		b.Fatalf("NewArenaOver: %v", err)
	}
	return arena
}

func benchColumn(b *testing.B, count int) (*Arena, uint32, ColumnarField) {
	b.Helper()
	arena := benchArena(b, uint32(count*4)+generated.ARENA_MIN_BYTES)
	values := make([]float32, count)
	for i := range values {
		values[i] = float32(i) * 1.5
	}
	field, err := WriteFloat32Column(arena, 0, values)
	if err != nil {
		b.Fatalf("WriteFloat32Column: %v", err)
	}
	batchID, err := WriteColumnarBatch(arena, uint32(count), []ColumnarField{field})
	if err != nil {
		b.Fatalf("WriteColumnarBatch: %v", err)
	}
	return arena, batchID, field
}

var columnSizes = []struct {
	name  string
	count int
}{
	{"1K", 1 << 10},
	{"64K", 1 << 16},
	{"1M", 1 << 20},
}

// BenchmarkColumnarDescriptorValidate measures the verifier alone: no value
// bytes are touched. Flat across the size series is the expected result.
func BenchmarkColumnarDescriptorValidate(b *testing.B) {
	for _, size := range columnSizes {
		b.Run(size.name, func(b *testing.B) {
			arena, batchID, _ := benchColumn(b, size.count)
			b.ReportAllocs()
			var rowSink uint32
			for b.Loop() {
				rows, fields, err := ReadColumnarBatch(arena, batchID)
				if err != nil || len(fields) != 1 {
					b.Fatalf("ReadColumnarBatch: %v", err)
				}
				rowSink = rows
			}
			if rowSink == 0 {
				b.Fatal("row count sank to zero; the benchmark measured nothing")
			}
		})
	}
}

// BenchmarkColumnarReadThenSum is the naive shape SumFloat32Column replaces:
// materialize the column, then reduce it with one accumulator.
func BenchmarkColumnarReadThenSum(b *testing.B) {
	for _, size := range columnSizes {
		b.Run(size.name, func(b *testing.B) {
			arena, _, field := benchColumn(b, size.count)
			b.SetBytes(int64(size.count) * 4)
			b.ReportAllocs()
			var sink float32
			for b.Loop() {
				values, err := ReadFloat32Column(arena, field)
				if err != nil {
					b.Fatalf("ReadFloat32Column: %v", err)
				}
				var sum float32
				for _, v := range values {
					sum += v
				}
				sink = sum
			}
			if sink == 0 {
				b.Fatal("sum sank to zero; the reduction was eliminated")
			}
		})
	}
}

// BenchmarkColumnarSumColumn measures the shipped reduction.
func BenchmarkColumnarSumColumn(b *testing.B) {
	for _, size := range columnSizes {
		b.Run(size.name, func(b *testing.B) {
			arena, _, field := benchColumn(b, size.count)
			b.SetBytes(int64(size.count) * 4)
			b.ReportAllocs()
			var sink float32
			for b.Loop() {
				sum, err := SumFloat32Column(arena, field)
				if err != nil {
					b.Fatalf("SumFloat32Column: %v", err)
				}
				sink = sum
			}
			if sink == 0 {
				b.Fatal("sum sank to zero; the reduction was eliminated")
			}
		})
	}
}

// BenchmarkColumnarReadOnly isolates the copying read from any reduction, so
// the allocation cost can be attributed separately from the arithmetic.
func BenchmarkColumnarReadOnly(b *testing.B) {
	for _, size := range columnSizes {
		b.Run(size.name, func(b *testing.B) {
			arena, _, field := benchColumn(b, size.count)
			b.SetBytes(int64(size.count) * 4)
			b.ReportAllocs()
			var sink int
			for b.Loop() {
				values, err := ReadFloat32Column(arena, field)
				if err != nil {
					b.Fatalf("ReadFloat32Column: %v", err)
				}
				sink = len(values)
			}
			if sink != size.count {
				b.Fatalf("read %d values, want %d", sink, size.count)
			}
		})
	}
}
