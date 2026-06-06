package bootstrap

import (
	"context"
	"fmt"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	metautil "github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/protoapi"
	"google.golang.org/protobuf/proto"
)

type DomainEventExecutor func(context.Context, string, extension.Object) (extension.Object, error)

func ObjectHandler(fn func(context.Context, extension.Object) (extension.Object, error)) HandlerFunc {
	return func(ctx context.Context, payload extension.Object) (any, error) {
		return fn(ctx, payload)
	}
}

func CommandHandler(fn func(context.Context, extension.Object) error) HandlerFunc {
	return func(ctx context.Context, payload extension.Object) (any, error) {
		return extension.Object{}, fn(ctx, payload)
	}
}

func BuildServiceHandlers(domain string, requestedEvents []string, handler *graceful.Handler, exec DomainEventExecutor) ServiceHandlers {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = "service"
	}
	handlers := ServiceHandlers{}
	for _, requestedEvent := range requestedEvents {
		eventType := requestedEvent
		handlers[eventType] = func(ctx context.Context, payload extension.Object) (any, error) {
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

				payload, err := protoapi.MessageToObject(request)
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
				logRes := finalizeResponse(domain, eventType, res.Clone())
				response, err := currentBinding.ResponseFromObject(res)
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

func ProtoHandler[T proto.Message](req T, resp proto.Message, handler func(context.Context, T) (proto.Message, error)) TypedHandlerRegistration {
	return TypedHandlerRegistration{
		Binding: protoapi.Binding{
			Request:  func() proto.Message { return proto.Clone(req) },
			Response: func() proto.Message { return proto.Clone(resp) },
		},
		Handler: func(ctx context.Context, msg proto.Message) (proto.Message, error) {
			typedMsg, ok := msg.(T)
			if !ok {
				return nil, fmt.Errorf("typed handler expected %T, got %T", req, msg)
			}
			return handler(ctx, typedMsg)
		},
	}
}

func finalizeResponse(domain, eventType string, res extension.Object) extension.Object {
	if res == nil {
		res = extension.Object{}
	}
	res["domain"] = extension.String(domain)
	res["event"] = extension.String(eventType)
	if _, ok := res["state"]; !ok {
		res["state"] = extension.String("success")
	}
	return res
}

func extractEntityID(payload extension.Object) string {
	if payload == nil {
		return ""
	}
	if id, ok := payload.GetString("public_id"); ok && id != "" {
		return id
	}
	for k, v := range payload {
		if strings.HasSuffix(k, "_public_id") || strings.HasSuffix(k, "_id") {
			if id, ok := v.StringValue(); ok && id != "" {
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
	handler.Success(ctx, action, message, result, metautil.FromContext(ctx).ToObject(), entityID, nil)
}

func emitError(handler *graceful.Handler, ctx context.Context, action, message string, err error, entityID string) {
	if handler == nil {
		return
	}
	handler.Error(ctx, action, message, err, metautil.FromContext(ctx).ToObject(), entityID)
}
