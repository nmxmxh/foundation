package contracttest

import (
	"context"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
)

type producerFunc func(context.Context, string) (events.Envelope, error)

func (f producerFunc) ProduceContractEvent(ctx context.Context, eventType string) (events.Envelope, error) {
	return f(ctx, eventType)
}

type consumerFunc func(context.Context, events.Envelope) error

func (f consumerFunc) ConsumeContractEvent(ctx context.Context, env events.Envelope) error {
	return f(ctx, env)
}

func TestVerifyProducerAndConsumer(t *testing.T) {
	producer := producerFunc(func(_ context.Context, eventType string) (events.Envelope, error) {
		return events.Envelope{EventType: eventType, Payload: map[string]any{"id": "1"}}, nil
	})
	consumerCalled := false
	consumer := consumerFunc(func(_ context.Context, env events.Envelope) error {
		consumerCalled = env.Payload["id"] == "1"
		return nil
	})

	if err := VerifyProducer(context.Background(), "order:created:v1:requested", producer); err != nil {
		t.Fatalf("VerifyProducer() error = %v", err)
	}
	if err := VerifyConsumer(context.Background(), "order:created:v1:requested", producer, consumer); err != nil {
		t.Fatalf("VerifyConsumer() error = %v", err)
	}
	if !consumerCalled {
		t.Fatalf("consumer was not called")
	}
}

func TestVerifyProducerRejectsSplitCorrelationIDs(t *testing.T) {
	producer := producerFunc(func(_ context.Context, eventType string) (events.Envelope, error) {
		return events.Envelope{
			EventType:     eventType,
			Payload:       map[string]any{"id": "1"},
			Metadata:      map[string]any{"correlation_id": "corr_metadata"},
			CorrelationID: "corr_envelope",
		}, nil
	})

	if err := VerifyProducer(context.Background(), "order:created:v1:requested", producer); err == nil {
		t.Fatal("expected split correlation IDs to fail producer contract")
	}
}
