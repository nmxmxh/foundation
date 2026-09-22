package placement

import (
	"context"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

// Execute uses resident input directly and writes into caller-owned output.
// The caller supplies a deadline. The operation must cooperate with cancellation.
func (r *BindingRegistry) Execute(ctx context.Context, request RuntimeBindingRequest, correlation string, output []byte) (RuntimeBindingReceipt, error) {
	if len(correlation) == 0 || len(correlation) > 128 {
		return RuntimeBindingReceipt{}, ErrBindingInvalid
	}
	ctx = bindingExecutionContext(ctx, correlation)
	resource, slot, version, err := r.begin(ctx, request, correlation, uint64(len(output)))
	if err != nil {
		return RuntimeBindingReceipt{}, err
	}
	defer r.finish(resource, slot)
	return r.run(ctx, request, resource, version, output)
}

func bindingExecutionContext(ctx context.Context, correlation string) context.Context {
	md, ok := metadata.FromContextOK(ctx)
	if ok && md.CorrelationID == correlation {
		return ctx
	}
	if !ok {
		md = metadata.New()
	}
	md.CorrelationID = correlation
	return metadata.IntoContext(ctx, md)
}

func (r *BindingRegistry) begin(ctx context.Context, request RuntimeBindingRequest, correlation string, capacity uint64) (*resourceVersion, *bindingSlot, *bindingVersion, error) {
	if len(correlation) == 0 || len(correlation) > 128 || request.SchemaVersion != BindingSchemaVersion ||
		request.RegistryID != r.id || request.OutputCapacity == 0 || request.OutputCapacity > MaxBindingOutputBytes ||
		uint64(request.OutputCapacity) > capacity || request.TimeoutMillis == 0 || request.TimeoutMillis > MaxBindingTimeoutMillis {
		return nil, nil, nil, ErrBindingInvalid
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > time.Duration(request.TimeoutMillis)*time.Millisecond {
		return nil, nil, nil, ErrBindingInvalid
	}
	tenant, err := r.authority(ctx, BindingExecute, request.ResourceID, request.BindingID)
	if err != nil {
		return nil, nil, nil, err
	}
	return r.acquire(tenant, request)
}

func (r *BindingRegistry) run(ctx context.Context, request RuntimeBindingRequest, resource *resourceVersion, version *bindingVersion, output []byte) (RuntimeBindingReceipt, error) {
	var receipt RuntimeBindingReceipt
	written, err := invokeBinding(ctx, version.spec.Operation, resource.data, output[:request.OutputCapacity:request.OutputCapacity])
	if err != nil {
		return receipt, err
	}
	if err = ctx.Err(); err != nil {
		return receipt, err
	}
	if written > request.OutputCapacity {
		return receipt, ErrBindingBudget
	}
	if err := validateBindingValue(r.types[version.spec.OutputTypeID], output[:written]); err != nil {
		return receipt, err
	}
	return RuntimeBindingReceipt{SchemaVersion: BindingSchemaVersion, BindingRevision: version.revision,
		ResourceGeneration: resource.generation, OutputTypeID: version.spec.OutputTypeID, OutputBytes: written,
		InputCopiedBytes: uint64(len(resource.data)) * uint64(version.spec.InputCopies)}, nil
}

func invokeBinding(ctx context.Context, operation BindingOperation, input, output []byte) (written uint32, err error) {
	defer func() {
		if recover() != nil {
			written, err = 0, ErrBindingExecution
		}
	}()
	return operation(ctx, input, output)
}

func validateBindingValue(validate func([]byte) error, data []byte) (err error) {
	if validate == nil {
		return ErrBindingInvalid
	}
	defer func() {
		if recover() != nil {
			err = ErrBindingExecution
		}
	}()
	if validate(data) != nil {
		return ErrBindingInvalid
	}
	return nil
}

func (r *BindingRegistry) acquire(tenant string, request RuntimeBindingRequest) (*resourceVersion, *bindingSlot, *bindingVersion, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, nil, nil, ErrBindingBusy
	}
	resource := r.resources[bindingKey{tenant, request.ResourceID}]
	slot := r.bindings[bindingKey{tenant, request.BindingID}]
	if resource == nil || slot == nil || resource.generation != request.ResourceGeneration || slot.current.revision != request.BindingRevision {
		return nil, nil, nil, ErrBindingStale
	}
	version := slot.current
	spec := version.spec
	if spec.ResourceID != request.ResourceID || spec.ResourceGeneration != request.ResourceGeneration ||
		spec.InputTypeID != request.InputTypeID || resource.typeID != request.InputTypeID || spec.OutputTypeID != request.OutputTypeID {
		return nil, nil, nil, ErrBindingInvalid
	}
	if request.OutputCapacity > spec.MaxOutputBytes || uint64(len(resource.data)) > spec.MaxInputBytes ||
		request.MaxInputCopyBytes < uint64(len(resource.data))*uint64(spec.InputCopies) {
		return nil, nil, nil, ErrBindingBudget
	}
	if r.inFlight >= r.limits.InFlight || slot.inFlight >= spec.MaxConcurrency {
		return nil, nil, nil, ErrBindingBusy
	}
	resource.readers++
	slot.inFlight++
	r.inFlight++
	return resource, slot, version, nil
}

func (r *BindingRegistry) finish(resource *resourceVersion, slot *bindingSlot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	resource.readers--
	slot.inFlight--
	r.inFlight--
	r.releaseRetired(resource)
}
