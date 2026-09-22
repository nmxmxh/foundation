package placement

import (
	"context"
	"errors"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
)

const (
	BindingRequestedEvent = "compute:binding:requested"
	BindingSuccessEvent   = "compute:binding:success"
	BindingFailedEvent    = "compute:binding:failed"
)

// BindingFrameDispatcher uses an existing authenticated transport connection.
// Endpoint discovery does not grant resource access.
type BindingFrameDispatcher func(context.Context, grpcsvc.Frame) (grpcsvc.Frame, error)

// BindingFrameHandler executes references against resources resident on this server.
// Authentication middleware must supply the organization context before dispatch.
func BindingFrameHandler(registry *BindingRegistry) grpcsvc.FrameHandler {
	return func(ctx context.Context, frame grpcsvc.Frame) (grpcsvc.Frame, error) {
		request, err := DecodeRuntimeBindingRequest(frame.Payload)
		if err != nil || registry == nil || frame.EventType != BindingRequestedEvent || frame.SchemaVersion != "1" ||
			len(frame.CorrelationID) == 0 || len(frame.CorrelationID) > 128 ||
			request.TimeoutMillis == 0 || request.TimeoutMillis > MaxBindingTimeoutMillis {
			return bindingFailure(frame.CorrelationID, ErrBindingInvalid), nil
		}
		if request.OutputCapacity == 0 || request.OutputCapacity > MaxBindingOutputBytes ||
			request.MaxTransferBytes < uint64(request.OutputCapacity)+RuntimeBindingRequestBytes+RuntimeBindingReceiptBytes {
			return bindingFailure(frame.CorrelationID, ErrBindingBudget), nil
		}
		ctx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutMillis)*time.Millisecond)
		defer cancel()
		ctx = bindingExecutionContext(ctx, frame.CorrelationID)
		resource, slot, version, err := registry.begin(ctx, request, frame.CorrelationID, uint64(request.OutputCapacity))
		if err != nil {
			return bindingFailure(frame.CorrelationID, err), nil
		}
		defer registry.finish(resource, slot)
		payload := make([]byte, RuntimeBindingReceiptBytes+int(request.OutputCapacity))
		receipt, err := registry.run(ctx, request, resource, version, payload[RuntimeBindingReceiptBytes:])
		if err != nil {
			return bindingFailure(frame.CorrelationID, err), nil
		}
		receipt.TransferredBytes = uint64(receipt.OutputBytes) + RuntimeBindingRequestBytes + RuntimeBindingReceiptBytes
		if err := receipt.EncodeTo(payload[:RuntimeBindingReceiptBytes]); err != nil {
			return grpcsvc.Frame{}, err
		}
		return grpcsvc.Frame{EventType: BindingSuccessEvent, CorrelationID: frame.CorrelationID,
			SchemaVersion: "1", Payload: payload[:RuntimeBindingReceiptBytes+int(receipt.OutputBytes)]}, nil
	}
}

// ExecuteRemoteBinding sends one reference and receives a bounded result without automatic retries.
// The transfer budget covers application payload bytes, excluding transport headers and encryption.
func ExecuteRemoteBinding(ctx context.Context, dispatch BindingFrameDispatcher, request RuntimeBindingRequest, correlation string) (RuntimeBindingReceipt, []byte, error) {
	var receipt RuntimeBindingReceipt
	if dispatch == nil || len(correlation) == 0 || len(correlation) > 128 || request.SchemaVersion != BindingSchemaVersion ||
		request.TimeoutMillis == 0 || request.TimeoutMillis > MaxBindingTimeoutMillis ||
		request.OutputCapacity == 0 || request.OutputCapacity > MaxBindingOutputBytes {
		return receipt, nil, ErrBindingInvalid
	}
	if request.MaxTransferBytes < uint64(request.OutputCapacity)+RuntimeBindingRequestBytes+RuntimeBindingReceiptBytes {
		return receipt, nil, ErrBindingBudget
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutMillis)*time.Millisecond)
	defer cancel()
	payload := make([]byte, RuntimeBindingRequestBytes)
	if err := request.EncodeTo(payload); err != nil {
		return receipt, nil, err
	}
	response, err := dispatch(ctx, grpcsvc.Frame{EventType: BindingRequestedEvent, Payload: payload, CorrelationID: correlation, SchemaVersion: "1"})
	if err != nil {
		return receipt, nil, err
	}
	if err := ctx.Err(); err != nil {
		return receipt, nil, err
	}
	if response.CorrelationID != correlation || response.SchemaVersion != "1" || len(response.Payload) < RuntimeBindingReceiptBytes {
		return receipt, nil, ErrBindingInvalid
	}
	receipt, err = DecodeRuntimeBindingReceipt(response.Payload[:RuntimeBindingReceiptBytes])
	if err != nil || receipt.SchemaVersion != BindingSchemaVersion {
		return RuntimeBindingReceipt{}, nil, ErrBindingInvalid
	}
	if response.EventType == BindingFailedEvent && len(response.Payload) == RuntimeBindingReceiptBytes && receipt.Status != 0 {
		return receipt, nil, bindingStatusError(receipt.Status)
	}
	output := response.Payload[RuntimeBindingReceiptBytes:]
	if response.EventType != BindingSuccessEvent || receipt.Status != 0 || receipt.BindingRevision != request.BindingRevision ||
		receipt.ResourceGeneration != request.ResourceGeneration || receipt.OutputTypeID != request.OutputTypeID ||
		uint64(len(output)) != uint64(receipt.OutputBytes) || receipt.OutputBytes > request.OutputCapacity ||
		receipt.TransferredBytes != uint64(len(output))+RuntimeBindingRequestBytes+RuntimeBindingReceiptBytes ||
		receipt.TransferredBytes > request.MaxTransferBytes || receipt.InputCopiedBytes > request.MaxInputCopyBytes {
		return RuntimeBindingReceipt{}, nil, ErrBindingInvalid
	}
	return receipt, output, nil
}

func bindingFailure(correlation string, cause error) grpcsvc.Frame {
	status := uint32(7)
	for i, candidate := range []error{ErrBindingInvalid, ErrBindingStale, ErrBindingForbidden, ErrBindingBudget, ErrBindingBusy, context.DeadlineExceeded, ErrBindingExecution} {
		if errors.Is(cause, candidate) || i == 5 && errors.Is(cause, context.Canceled) {
			status = uint32(i + 1)
			break
		}
	}
	payload := make([]byte, RuntimeBindingReceiptBytes)
	receipt := RuntimeBindingReceipt{SchemaVersion: BindingSchemaVersion, Status: status}
	if err := receipt.EncodeTo(payload); err != nil {
		return grpcsvc.Frame{}
	}
	return grpcsvc.Frame{EventType: BindingFailedEvent, CorrelationID: correlation, SchemaVersion: "1", Payload: payload}
}

func bindingStatusError(status uint32) error {
	values := [...]error{ErrBindingInvalid, ErrBindingStale, ErrBindingForbidden, ErrBindingBudget, ErrBindingBusy, context.DeadlineExceeded, ErrBindingExecution}
	if status == 0 || status > uint32(len(values)) {
		return ErrBindingInvalid
	}
	return values[status-1]
}
