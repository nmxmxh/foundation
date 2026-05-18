package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	"google.golang.org/protobuf/proto"
)

// FrameRouterAdapter is the minimal frame registration interface used by
// Foundation's internal synchronous plane.
type FrameRouterAdapter interface {
	RegisterFrame(eventType string, handler grpcsvc.FrameHandler) error
}

// RegisterTypedFrameHandlers projects typed protobuf service bindings into the
// frame router. It is the internal synchronous counterpart to RegisterTypedHandlers:
// the same binding decodes protobuf bytes, executes the typed handler under the
// standard bounded execution controller, and returns protobuf bytes in a Frame.
func RegisterTypedFrameHandlers(router FrameRouterAdapter, handlers TypedServiceHandlers, opts ...ConcurrencyOptions) error {
	if router == nil {
		return errors.New("frame router is required")
	}

	regOpts := defaultConcurrencyOptions()
	if len(opts) > 0 {
		regOpts = normalizeConcurrencyOptions(opts[0])
	}

	for eventType, registration := range handlers {
		if registration.Handler == nil {
			return fmt.Errorf("nil typed frame handler for event_type %s", eventType)
		}
		if err := validateRequestEventType(eventType); err != nil {
			return err
		}
		if err := registration.Binding.Validate(); err != nil {
			return fmt.Errorf("invalid protobuf binding for %s: %w", eventType, err)
		}

		currentBinding := registration.Binding
		controller := NewHandlerExecutionController(regOpts)
		wrapped := controller.WrapTyped(registration.Handler)
		var requestPool *sync.Pool
		if currentBinding.AllowsProtobufDecodeReuse() {
			requestPool = &sync.Pool{
				New: func() any {
					msg, err := currentBinding.NewRequest()
					if err != nil {
						return nil
					}
					return msg
				},
			}
		}

		if err := router.RegisterFrame(eventType, func(ctx context.Context, frame grpcsvc.Frame) (grpcsvc.Frame, error) {
			request, pooled, err := decodeFrameRequest(currentBinding, requestPool, frame.Payload, frameMetadata(frame))
			if err != nil {
				return grpcsvc.Frame{}, err
			}

			response, err := wrapped(ctx, request)
			if err != nil {
				releaseFrameRequest(requestPool, request, pooled)
				return grpcsvc.Frame{}, err
			}

			payload, err := currentBinding.EncodeResponseBytes(response)
			if err != nil {
				releaseFrameRequest(requestPool, request, pooled)
				return grpcsvc.Frame{}, err
			}
			releaseFrameRequest(requestPool, request, pooled)

			return grpcsvc.Frame{
				EventType:     frame.EventType,
				Payload:       payload,
				CorrelationID: frame.CorrelationID,
				SchemaVersion: frame.SchemaVersion,
			}, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// RegisterTypedPlanes registers one typed service binding map into both runtime
// projections: registry protobuf dispatch and internal frame dispatch.
func RegisterTypedPlanes(registry RegistryAdapter, router FrameRouterAdapter, handlers TypedServiceHandlers, opts ...ConcurrencyOptions) error {
	if err := RegisterTypedHandlers(registry, handlers, opts...); err != nil {
		return err
	}
	return RegisterTypedFrameHandlers(router, handlers, opts...)
}

func frameMetadata(frame grpcsvc.Frame) map[string]any {
	correlationID := strings.TrimSpace(frame.CorrelationID)
	if correlationID == "" {
		return nil
	}
	return map[string]any{"correlation_id": correlationID}
}

func decodeFrameRequest(binding protoapi.Binding, requestPool *sync.Pool, payload []byte, metadata map[string]any) (proto.Message, bool, error) {
	if requestPool == nil {
		msg, err := binding.DecodeRequestBytes(payload, metadata)
		return msg, false, err
	}
	request, ok := requestPool.Get().(proto.Message)
	if !ok || request == nil {
		var err error
		request, err = binding.NewRequest()
		if err != nil {
			return nil, false, err
		}
	}
	msg, err := binding.DecodeRequestBytesInto(request, payload, metadata, protoapi.DecodeRequestBytesIntoOptions{
		CompleteMessage: true,
	})
	if err != nil {
		proto.Reset(request)
		requestPool.Put(request)
		return nil, false, err
	}
	return msg, true, nil
}

func releaseFrameRequest(requestPool *sync.Pool, request proto.Message, pooled bool) {
	if !pooled || request == nil || requestPool == nil {
		return
	}
	requestPool.Put(request)
}
