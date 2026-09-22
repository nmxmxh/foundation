package runtimehost

import (
	"context"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/placement"
)

// NewFFIBinding connects an existing native unit to checked local and remote execution.
// The FFI control buffer copies input once. Zero-copy requirements therefore reject this adapter.
// Unit internals remain trusted and require their own allocation and driver evidence.
func NewFFIBinding(pool *FFIPool, unitID string, spec placement.BindingSpec) (placement.BindingSpec, error) {
	if pool == nil || unitID == "" || len(unitID) > 128 || spec.MaxInputBytes == 0 ||
		spec.MaxInputBytes > uint64(generated.INPUT_MAX_BYTES) || spec.MaxOutputBytes == 0 ||
		spec.MaxOutputBytes > generated.OUTPUT_MAX_BYTES {
		return placement.BindingSpec{}, placement.ErrBindingInvalid
	}
	spec.InputCopies = 1
	spec.InputCopiesKnown = true
	spec.Operation = func(ctx context.Context, input, output []byte) (uint32, error) {
		response, err := pool.ExecuteInto(ctx, ProcessRequest{UnitID: unitID, Input: input}, output)
		if err != nil {
			return 0, err
		}
		return uint32(len(response.Output)), nil
	}
	return spec, nil
}
