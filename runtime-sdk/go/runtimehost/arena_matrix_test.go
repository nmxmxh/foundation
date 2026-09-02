package runtimehost

import (
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

func TestWriteFloat32MatrixKeepsOneContiguousColumn(t *testing.T) {
	arena, err := NewArenaOver(make([]byte, generated.ARENA_MIN_BYTES))
	if err != nil {
		t.Fatalf("NewArenaOver() error = %v", err)
	}
	matrix, err := WriteFloat32Matrix(arena, 7, 2, 3, []float32{1, 2, 3, 4, 5, 6})
	if err != nil {
		t.Fatalf("WriteFloat32Matrix() error = %v", err)
	}
	if matrix.Field.Length != 6 || matrix.Rows != 2 || matrix.Dimensions != 3 {
		t.Fatalf("matrix = %+v", matrix)
	}
	values, err := ReadFloat32Column(arena, matrix.Field)
	if err != nil {
		t.Fatalf("ReadFloat32Column() error = %v", err)
	}
	if values[0] != 1 || values[5] != 6 {
		t.Fatalf("values = %v", values)
	}
}

func TestWriteFloat32MatrixRejectsInvalidShape(t *testing.T) {
	arena, err := NewArenaOver(make([]byte, generated.ARENA_MIN_BYTES))
	if err != nil {
		t.Fatalf("NewArenaOver() error = %v", err)
	}
	if _, err := WriteFloat32Matrix(arena, 7, 2, 3, []float32{1}); err == nil {
		t.Fatal("WriteFloat32Matrix() accepted wrong value count")
	}
	if _, err := WriteFloat32Matrix(arena, 7, 0, 3, nil); err == nil {
		t.Fatal("WriteFloat32Matrix() accepted zero rows")
	}
}
