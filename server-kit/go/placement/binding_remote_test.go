package placement

import (
	"context"
	"errors"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

func TestRemoteBinding(t *testing.T) {
	r, ctx, request, _ := bindingFixture(t, nil)
	handler := BindingFrameHandler(r)
	dispatch := func(ctx context.Context, frame grpcsvc.Frame) (grpcsvc.Frame, error) {
		if len(frame.Payload) != RuntimeBindingRequestBytes {
			t.Fatal("input data crossed transport")
		}
		return handler(ctx, frame)
	}
	receipt, output, err := ExecuteRemoteBinding(ctx, dispatch, request, "remote")
	if err != nil || string(output) != "resident" || receipt.TransferredBytes != request.MaxTransferBytes || receipt.InputCopiedBytes != 0 {
		t.Fatalf("remote: %+v %q %v", receipt, output, err)
	}
	if _, err := r.PublishResource(ctx, 7, 101, 1, []byte("new-data")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExecuteRemoteBinding(ctx, dispatch, request, "stale"); !errors.Is(err, ErrBindingStale) {
		t.Fatal(err)
	}
	if _, _, err := ExecuteRemoteBinding(security.ContextWithOrganizationID(ctx, ""), dispatch, request, "auth"); !errors.Is(err, ErrBindingForbidden) {
		t.Fatal(err)
	}
	request.MaxTransferBytes--
	if _, _, err := ExecuteRemoteBinding(ctx, dispatch, request, "budget"); !errors.Is(err, ErrBindingBudget) {
		t.Fatal(err)
	}
}

func TestRemoteBindingRejectsMalformedFrames(t *testing.T) {
	r, ctx, request, _ := bindingFixture(t, nil)
	payload := make([]byte, RuntimeBindingRequestBytes)
	if err := request.EncodeTo(payload); err != nil {
		t.Fatal(err)
	}
	frame := grpcsvc.Frame{EventType: BindingRequestedEvent, SchemaVersion: "1", CorrelationID: "bad", Payload: payload}
	for _, mutate := range []func(*grpcsvc.Frame){
		func(f *grpcsvc.Frame) { f.Payload = nil }, func(f *grpcsvc.Frame) { f.EventType = "wrong" },
		func(f *grpcsvc.Frame) { f.SchemaVersion = "2" }, func(f *grpcsvc.Frame) { f.CorrelationID = "" },
	} {
		v := frame
		mutate(&v)
		got, err := BindingFrameHandler(r)(ctx, v)
		if err != nil || got.EventType != BindingFailedEvent {
			t.Fatalf("frame: %+v %v", got, err)
		}
	}
	for _, mutate := range []func(*RuntimeBindingRequest){
		func(v *RuntimeBindingRequest) { v.TimeoutMillis = 0 }, func(v *RuntimeBindingRequest) { v.TimeoutMillis = MaxBindingTimeoutMillis + 1 },
		func(v *RuntimeBindingRequest) { v.OutputCapacity = 0 }, func(v *RuntimeBindingRequest) { v.OutputCapacity = MaxBindingOutputBytes + 1 },
		func(v *RuntimeBindingRequest) { v.MaxTransferBytes = 0 }, func(v *RuntimeBindingRequest) { v.RegistryID++ },
	} {
		v := request
		mutate(&v)
		if err := v.EncodeTo(payload); err != nil {
			t.Fatal(err)
		}
		got, err := BindingFrameHandler(r)(ctx, frame)
		if err != nil || got.EventType != BindingFailedEvent {
			t.Fatalf("frame: %+v %v", got, err)
		}
	}
	if got, _ := BindingFrameHandler(nil)(ctx, frame); got.EventType != BindingFailedEvent {
		t.Fatal(got)
	}
}

func TestRemoteBindingRejectsResponseSubstitution(t *testing.T) {
	r, ctx, request, _ := bindingFixture(t, nil)
	for _, mutate := range []func(*grpcsvc.Frame){
		func(f *grpcsvc.Frame) { f.CorrelationID = "other" }, func(f *grpcsvc.Frame) { f.SchemaVersion = "2" },
		func(f *grpcsvc.Frame) { f.EventType = "other" }, func(f *grpcsvc.Frame) { f.Payload = nil },
		func(f *grpcsvc.Frame) { f.Payload[0] = 1 }, func(f *grpcsvc.Frame) { f.Payload = append(f.Payload, 0) },
	} {
		dispatch := func(ctx context.Context, f grpcsvc.Frame) (grpcsvc.Frame, error) {
			res, err := BindingFrameHandler(r)(ctx, f)
			mutate(&res)
			return res, err
		}
		if _, _, err := ExecuteRemoteBinding(ctx, dispatch, request, "substitution"); !errors.Is(err, ErrBindingInvalid) {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*RuntimeBindingReceipt){
		func(v *RuntimeBindingReceipt) { v.SchemaVersion++ }, func(v *RuntimeBindingReceipt) { v.BindingRevision++ },
		func(v *RuntimeBindingReceipt) { v.ResourceGeneration++ }, func(v *RuntimeBindingReceipt) { v.OutputTypeID++ },
		func(v *RuntimeBindingReceipt) { v.OutputBytes++ }, func(v *RuntimeBindingReceipt) { v.TransferredBytes++ },
		func(v *RuntimeBindingReceipt) { v.InputCopiedBytes++ }, func(v *RuntimeBindingReceipt) { v.Status++ },
	} {
		dispatch := func(ctx context.Context, f grpcsvc.Frame) (grpcsvc.Frame, error) {
			res, err := BindingFrameHandler(r)(ctx, f)
			if err != nil {
				return res, err
			}
			receipt, err := DecodeRuntimeBindingReceipt(res.Payload[:RuntimeBindingReceiptBytes])
			if err != nil {
				return res, err
			}
			mutate(&receipt)
			err = receipt.EncodeTo(res.Payload[:RuntimeBindingReceiptBytes])
			return res, err
		}
		if _, _, err := ExecuteRemoteBinding(ctx, dispatch, request, "substitution"); !errors.Is(err, ErrBindingInvalid) {
			t.Fatal(err)
		}
	}
}

func TestBindingRemoteFailures(t *testing.T) {
	r, ctx, request, _ := bindingFixture(t, nil)
	for _, cause := range []error{ErrBindingInvalid, ErrBindingStale, ErrBindingForbidden, ErrBindingBudget, ErrBindingBusy, context.DeadlineExceeded, ErrBindingExecution, context.Canceled, errors.New("private failure")} {
		dispatch := func(_ context.Context, f grpcsvc.Frame) (grpcsvc.Frame, error) {
			return bindingFailure(f.CorrelationID, cause), nil
		}
		_, output, err := ExecuteRemoteBinding(ctx, dispatch, request, "failed")
		if err == nil || output != nil {
			t.Fatalf("failure exposed output: %q %v", output, err)
		}
	}
	for _, status := range []uint32{0, 8} {
		if !errors.Is(bindingStatusError(status), ErrBindingInvalid) {
			t.Fatal(status)
		}
	}
	if _, _, err := ExecuteRemoteBinding(ctx, nil, request, "nil"); !errors.Is(err, ErrBindingInvalid) {
		t.Fatal(err)
	}
	for _, mutate := range []func(*RuntimeBindingRequest){
		func(v *RuntimeBindingRequest) { v.SchemaVersion++ }, func(v *RuntimeBindingRequest) { v.TimeoutMillis = 0 },
		func(v *RuntimeBindingRequest) { v.TimeoutMillis = MaxBindingTimeoutMillis + 1 }, func(v *RuntimeBindingRequest) { v.OutputCapacity = 0 },
		func(v *RuntimeBindingRequest) { v.OutputCapacity = MaxBindingOutputBytes + 1 },
	} {
		v := request
		mutate(&v)
		if _, _, err := ExecuteRemoteBinding(ctx, BindingFrameDispatcher(BindingFrameHandler(r)), v, "invalid"); !errors.Is(err, ErrBindingInvalid) {
			t.Fatal(err)
		}
	}
	dispatch := func(context.Context, grpcsvc.Frame) (grpcsvc.Frame, error) { return grpcsvc.Frame{}, ErrBindingBusy }
	if _, _, err := ExecuteRemoteBinding(ctx, dispatch, request, "transport"); !errors.Is(err, ErrBindingBusy) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	dispatch = func(context.Context, grpcsvc.Frame) (grpcsvc.Frame, error) { cancel(); return grpcsvc.Frame{}, nil }
	if _, _, err := ExecuteRemoteBinding(canceled, dispatch, request, "canceled"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func BenchmarkBindingFrame(b *testing.B) {
	r, ctx, request, _ := bindingFixture(b, nil)
	handler := BindingFrameDispatcher(BindingFrameHandler(r))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := ExecuteRemoteBinding(ctx, handler, request, "benchmark"); err != nil {
			b.Fatal(err)
		}
	}
}
