package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
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

		if err := router.RegisterFrame(eventType, func(ctx context.Context, frame grpcsvc.Frame) (grpcsvc.Frame, error) {
			request, err := currentBinding.DecodeRequestBytes(frame.Payload, frameMetadata(frame))
			if err != nil {
				return grpcsvc.Frame{}, err
			}

			response, err := wrapped(ctx, request)
			if err != nil {
				return grpcsvc.Frame{}, err
			}

			payload, err := currentBinding.EncodeResponseBytes(response)
			if err != nil {
				return grpcsvc.Frame{}, err
			}

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
