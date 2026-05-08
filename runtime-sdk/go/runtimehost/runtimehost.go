package runtimehost

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

type Buffer struct {
	raw []byte
}

func NewBuffer(raw []byte) (*Buffer, error) {
	if len(raw) < int(generated.BUFFER_TOTAL_BYTES) {
		return nil, fmt.Errorf("runtime buffer too small: %d < %d", len(raw), generated.BUFFER_TOTAL_BYTES)
	}
	return &Buffer{raw: raw}, nil
}

func (b *Buffer) RawBytes() []byte {
	if b == nil {
		return nil
	}
	return b.raw
}

func (b *Buffer) Reset() {
	if b == nil {
		return
	}
	clear(b.raw)
}

func (b *Buffer) HeaderInt(index uint32) (int32, error) {
	if index >= generated.HEADER_INT_COUNT {
		return 0, fmt.Errorf("invalid header index %d", index)
	}
	offset := generated.OFFSET_HEADER_INTS + index*4
	return int32(binary.LittleEndian.Uint32(b.raw[offset : offset+4])), nil
}

func (b *Buffer) SetHeaderInt(index uint32, value int32) error {
	if index >= generated.HEADER_INT_COUNT {
		return fmt.Errorf("invalid header index %d", index)
	}
	offset := generated.OFFSET_HEADER_INTS + index*4
	binary.LittleEndian.PutUint32(b.raw[offset:offset+4], uint32(value))
	return nil
}

func (b *Buffer) LoadEpoch(index uint32) int32 {
	if index >= generated.EPOCH_SLOT_COUNT {
		return 0
	}
	offset := generated.OFFSET_EPOCHS + index*generated.EPOCH_SLOT_BYTES
	return atomic.LoadInt32((*int32)(unsafe.Pointer(&b.raw[offset])))
}

func (b *Buffer) StoreEpoch(index uint32, value int32) error {
	if index >= generated.EPOCH_SLOT_COUNT {
		return fmt.Errorf("invalid epoch index %d", index)
	}
	offset := generated.OFFSET_EPOCHS + index*generated.EPOCH_SLOT_BYTES
	atomic.StoreInt32((*int32)(unsafe.Pointer(&b.raw[offset])), value)
	return nil
}

func (b *Buffer) AddEpoch(index uint32, delta int32) (int32, error) {
	if index >= generated.EPOCH_SLOT_COUNT {
		return 0, fmt.Errorf("invalid epoch index %d", index)
	}
	offset := generated.OFFSET_EPOCHS + index*generated.EPOCH_SLOT_BYTES
	current := atomic.AddInt32((*int32)(unsafe.Pointer(&b.raw[offset])), delta)
	return current - delta, nil
}

func (b *Buffer) SetInputBytes(payload []byte) error {
	if len(payload) > int(generated.INPUT_MAX_BYTES) {
		return fmt.Errorf("input payload too large: %d > %d", len(payload), generated.INPUT_MAX_BYTES)
	}
	clear(b.raw[generated.OFFSET_INPUT_BYTES : generated.OFFSET_INPUT_BYTES+generated.INPUT_MAX_BYTES])
	copy(b.raw[generated.OFFSET_INPUT_BYTES:], payload)
	return b.SetHeaderInt(generated.INT_IDX_INPUT_LENGTH, int32(len(payload)))
}

func (b *Buffer) InputBytes() ([]byte, error) {
	length, err := b.HeaderInt(generated.INT_IDX_INPUT_LENGTH)
	if err != nil {
		return nil, err
	}
	if length < 0 || length > int32(generated.INPUT_MAX_BYTES) {
		return nil, fmt.Errorf("invalid input length %d", length)
	}
	start := generated.OFFSET_INPUT_BYTES
	end := start + uint32(length)
	return append([]byte(nil), b.raw[start:end]...), nil
}

func (b *Buffer) SetOutputBytes(payload []byte) error {
	if len(payload) > int(generated.OUTPUT_MAX_BYTES) {
		return fmt.Errorf("output payload too large: %d > %d", len(payload), generated.OUTPUT_MAX_BYTES)
	}
	clear(b.raw[generated.OFFSET_OUTPUT_BYTES : generated.OFFSET_OUTPUT_BYTES+generated.OUTPUT_MAX_BYTES])
	copy(b.raw[generated.OFFSET_OUTPUT_BYTES:], payload)
	return b.SetHeaderInt(generated.INT_IDX_OUTPUT_LENGTH, int32(len(payload)))
}

func (b *Buffer) OutputBytes() ([]byte, error) {
	length, err := b.HeaderInt(generated.INT_IDX_OUTPUT_LENGTH)
	if err != nil {
		return nil, err
	}
	if length < 0 || length > int32(generated.OUTPUT_MAX_BYTES) {
		return nil, fmt.Errorf("invalid output length %d", length)
	}
	start := generated.OFFSET_OUTPUT_BYTES
	end := start + uint32(length)
	return append([]byte(nil), b.raw[start:end]...), nil
}

func (b *Buffer) Initialize(moduleVersion int32) error {
	if err := b.SetHeaderInt(generated.INT_IDX_SCHEMA_VERSION, int32(generated.BUFFER_SCHEMA_VERSION)); err != nil {
		return err
	}
	if err := b.SetHeaderInt(generated.INT_IDX_INPUT_LENGTH, 0); err != nil {
		return err
	}
	if err := b.SetHeaderInt(generated.INT_IDX_OUTPUT_LENGTH, 0); err != nil {
		return err
	}
	if err := b.SetHeaderInt(generated.INT_IDX_STATUS_CODE, 0); err != nil {
		return err
	}
	if err := b.SetHeaderInt(generated.INT_IDX_CONTEXT_HASH, 0); err != nil {
		return err
	}
	if err := b.SetHeaderInt(generated.INT_IDX_MODULE_VERSION, moduleVersion); err != nil {
		return err
	}
	if err := b.StoreEpoch(generated.IDX_KERNEL_READY, 1); err != nil {
		return err
	}
	return nil
}

func (b *Buffer) SetDiagnosticsText(message string) error {
	bytes := []byte(message)
	if len(bytes) > int(generated.DIAGNOSTIC_MAX_BYTES) {
		return fmt.Errorf("diagnostic payload too large: %d > %d", len(bytes), generated.DIAGNOSTIC_MAX_BYTES)
	}
	clear(b.raw[generated.OFFSET_DIAGNOSTIC_BYTES : generated.OFFSET_DIAGNOSTIC_BYTES+generated.DIAGNOSTIC_MAX_BYTES])
	copy(b.raw[generated.OFFSET_DIAGNOSTIC_BYTES:], bytes)
	_, err := b.AddEpoch(generated.IDX_DIAGNOSTICS_WRITTEN, 1)
	return err
}

func (b *Buffer) DiagnosticsText() string {
	payload := b.raw[generated.OFFSET_DIAGNOSTIC_BYTES : generated.OFFSET_DIAGNOSTIC_BYTES+generated.DIAGNOSTIC_MAX_BYTES]
	return strings.TrimRight(string(payload), "\x00")
}
