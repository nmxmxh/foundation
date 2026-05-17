package bootstrap

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	metautil "github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	"google.golang.org/protobuf/proto"
)

type DomainEventExecutor func(context.Context, string, map[string]any) (map[string]any, error)

func BuildServiceHandlers(domain string, requestedEvents []string, handler *graceful.Handler, exec DomainEventExecutor) ServiceHandlers {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = "service"
	}
	handlers := ServiceHandlers{}
	for _, requestedEvent := range requestedEvents {
		eventType := requestedEvent
		handlers[eventType] = func(ctx context.Context, payload map[string]any) (any, error) {
			action := strings.TrimSuffix(strings.TrimSpace(eventType), ":requested")
			entityID := extractEntityID(payload)
			if payload == nil {
				err := fmt.Errorf("payload is required")
				emitError(handler, ctx, action, "payload validation failed", err, "")
				return nil, err
			}

			res, err := exec(ctx, eventType, payload)
			if err != nil {
				emitError(handler, ctx, action, fmt.Sprintf("%s command failed", domain), err, entityID)
				return nil, err
			}
			res = finalizeResponse(domain, eventType, res)
			if resEntity := extractEntityID(res); resEntity != "" {
				entityID = resEntity
			}
			emitSuccess(handler, ctx, action, fmt.Sprintf("%s command processed", domain), res, entityID)
			return res, nil
		}
	}
	return handlers
}

func BuildTypedServiceHandlers(domain string, bindings map[string]protoapi.Binding, handler *graceful.Handler, exec DomainEventExecutor) TypedServiceHandlers {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = "service"
	}

	handlers := TypedServiceHandlers{}
	for requestedEvent, binding := range bindings {
		eventType := requestedEvent
		currentBinding := binding
		handlers[eventType] = TypedHandlerRegistration{
			Binding: currentBinding,
			Handler: func(ctx context.Context, request proto.Message) (proto.Message, error) {
				action := strings.TrimSuffix(strings.TrimSpace(eventType), ":requested")
				if request == nil {
					err := fmt.Errorf("request is required")
					emitError(handler, ctx, action, "request validation failed", err, "")
					return nil, err
				}

				payload, err := protoapi.MessageToMap(request)
				if err != nil {
					emitError(handler, ctx, action, fmt.Sprintf("%s request decode failed", domain), err, "")
					return nil, err
				}

				entityID := extractEntityID(payload)

				res, err := exec(ctx, eventType, payload)
				if err != nil {
					emitError(handler, ctx, action, fmt.Sprintf("%s command failed", domain), err, entityID)
					return nil, err
				}
				logRes := finalizeResponse(domain, eventType, cloneShallowMap(res))
				response, err := currentBinding.ResponseFromMap(res)
				if err != nil {
					emitError(handler, ctx, action, fmt.Sprintf("%s response encode failed", domain), err, entityID)
					return nil, err
				}
				if resEntity := extractEntityID(res); resEntity != "" {
					entityID = resEntity
				}
				emitSuccess(handler, ctx, action, fmt.Sprintf("%s command processed", domain), logRes, entityID)
				return response, nil
			},
		}
	}
	return handlers
}

func finalizeResponse(domain, eventType string, res map[string]any) map[string]any {
	if res == nil {
		res = map[string]any{}
	}
	res["domain"] = domain
	res["event"] = eventType
	if _, ok := res["state"]; !ok {
		res["state"] = "success"
	}
	return res
}

func cloneShallowMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	maps.Copy(out, input)
	return out
}

func extractEntityID(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if id, ok := payload["public_id"].(string); ok && id != "" {
		return id
	}
	for k, v := range payload {
		if strings.HasSuffix(k, "_public_id") || strings.HasSuffix(k, "_id") {
			if id, ok := v.(string); ok && id != "" {
				return id
			}
		}
	}
	return ""
}

func emitSuccess(handler *graceful.Handler, ctx context.Context, action, message string, result any, entityID string) {
	if handler == nil {
		return
	}
	handler.Success(ctx, action, message, result, metautil.FromContext(ctx).ToMap(), entityID, nil)
}

func emitError(handler *graceful.Handler, ctx context.Context, action, message string, err error, entityID string) {
	if handler == nil {
		return
	}
	handler.Error(ctx, action, message, err, metautil.FromContext(ctx).ToMap(), entityID)
}
