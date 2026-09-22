package placement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
)

func largeBindingFixture(tb testing.TB) (*BindingRegistry, context.Context, RuntimeBindingRequest, BindingOperation, []byte) {
	tb.Helper()
	ctx := bindingContext(tb)
	input := bytes.Repeat([]byte{1}, 1<<20)
	validator := func(size int) func([]byte) error {
		return func(data []byte) error {
			if len(data) != size {
				return ErrBindingInvalid
			}
			return nil
		}
	}
	r, err := NewBindingRegistry(1, BindingLimits{1, 1, 2 << 20, 1}, func(context.Context, BindingAction, uint64, uint64) error { return nil }, BindingType{1, validator(len(input))}, BindingType{2, validator(8)})
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := r.PublishResource(ctx, 1, 1, 0, input); err != nil {
		tb.Fatal(err)
	}
	operation := func(_ context.Context, input, output []byte) (uint32, error) {
		var sum uint64
		for _, value := range input {
			sum += uint64(value)
		}
		binary.LittleEndian.PutUint64(output, sum)
		return 8, nil
	}
	request, err := r.Bind(ctx, BindingSpec{InputCopiesKnown: true, ID: 1, ResourceID: 1, ResourceGeneration: 1, InputTypeID: 1, OutputTypeID: 2, MaxInputBytes: uint64(len(input)), MaxOutputBytes: 8, MaxConcurrency: 1, Operation: operation}, 0)
	if err != nil {
		tb.Fatal(err)
	}
	return r, ctx, request, operation, input
}

func TestLargeBindingKeepsInputResident(t *testing.T) {
	r, ctx, request, _, _ := largeBindingFixture(t)
	handler := BindingFrameHandler(r)
	dispatch := func(ctx context.Context, frame grpcsvc.Frame) (grpcsvc.Frame, error) {
		if len(frame.Payload) != 104 {
			t.Fatalf("request transferred %d bytes", len(frame.Payload))
		}
		return handler(ctx, frame)
	}
	receipt, output, err := ExecuteRemoteBinding(ctx, dispatch, request, "large-resident")
	if err != nil || binary.LittleEndian.Uint64(output) != 1<<20 || receipt.TransferredBytes != 184 || receipt.InputCopiedBytes != 0 {
		t.Fatalf("resident reduction: %+v %x %v", receipt, output, err)
	}
}

func BenchmarkBindingMegabyte(b *testing.B) {
	r, ctx, request, operation, input := largeBindingFixture(b)
	var output [8]byte
	digest := sha256.Sum256(input)
	ticket := RemoteComputeTicket{ProjectionID: "resident", ScopeKey: "tenant-a", PayloadLen: uint32(len(input)), ChecksumHex: hex.EncodeToString(digest[:]), RequiredClassMask: 1, DeadlineNs: 1}
	b.Run("direct", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(input)))
		for b.Loop() {
			if _, err := operation(ctx, input, output[:]); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("resident", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(input)))
		for b.Loop() {
			if _, err := r.Execute(ctx, request, "binding-test", output[:]); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("inline-ticket", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(input)))
		for b.Loop() {
			frame, err := EncodeRemoteComputeRequest(ticket, input)
			if err != nil {
				b.Fatal(err)
			}
			_, data, err := DecodeRemoteComputeRequest(frame)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := operation(ctx, data, output[:]); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("publish", func(b *testing.B) {
		publisher, publicationContext, _, _, publicationInput := largeBindingFixture(b)
		b.ReportAllocs()
		b.SetBytes(int64(len(input)))
		generation := uint64(1)
		for b.Loop() {
			next, err := publisher.PublishResource(publicationContext, 1, 1, generation, publicationInput)
			if err != nil {
				b.Fatal(err)
			}
			generation = next
		}
	})
}
