package contracttest

import (
	"context"
	"errors"
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
		return events.Envelope{EventType: eventType, Payload: contractObject(map[string]any{"id": "1"})}, nil
	})
	consumerCalled := false
	consumer := consumerFunc(func(_ context.Context, env events.Envelope) error {
		id, _ := env.Payload.GetString("id")
		consumerCalled = id == "1"
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
			Payload:       contractObject(map[string]any{"id": "1"}),
			Metadata:      contractObject(map[string]any{"correlation_id": "corr_metadata"}),
			CorrelationID: "corr_envelope",
		}, nil
	})

	if err := VerifyProducer(context.Background(), "order:created:v1:requested", producer); err == nil {
		t.Fatal("expected split correlation IDs to fail producer contract")
	}
}

func TestVerifyProducerAndConsumerErrorPaths(t *testing.T) {
	if err := VerifyProducer(context.Background(), "order:created:v1:requested", nil); err == nil {
		t.Fatalf("expected nil producer error")
	}
	producerErr := errors.New("produce failed")
	failingProducer := producerFunc(func(context.Context, string) (events.Envelope, error) {
		return events.Envelope{}, producerErr
	})
	if err := VerifyProducer(context.Background(), "order:created:v1:requested", failingProducer); !errors.Is(err, producerErr) {
		t.Fatalf("producer error = %v", err)
	}
	wrongTypeProducer := producerFunc(func(context.Context, string) (events.Envelope, error) {
		return events.Envelope{EventType: "order:wrong:v1:requested", Payload: contractObject(map[string]any{"id": "1"})}, nil
	})
	if err := VerifyProducer(context.Background(), "order:created:v1:requested", wrongTypeProducer); err == nil {
		t.Fatalf("expected wrong event type error")
	}
	invalidProducer := producerFunc(func(context.Context, string) (events.Envelope, error) {
		return events.Envelope{EventType: "bad event", Payload: contractObject(map[string]any{"id": "1"})}, nil
	})
	if err := VerifyProducer(context.Background(), "bad event", invalidProducer); err == nil {
		t.Fatalf("expected invalid envelope error")
	}

	if err := VerifyConsumer(context.Background(), "order:created:v1:requested", failingProducer, consumerFunc(func(context.Context, events.Envelope) error {
		return nil
	})); !errors.Is(err, producerErr) {
		t.Fatalf("consumer producer error = %v", err)
	}
	if err := VerifyConsumer(context.Background(), "order:created:v1:requested", wrongTypeProducer, nil); err == nil {
		t.Fatalf("expected nil consumer error")
	}
	if err := VerifyConsumer(context.Background(), "order:created:v1:requested", invalidProducer, consumerFunc(func(context.Context, events.Envelope) error {
		return nil
	})); err == nil {
		t.Fatalf("expected invalid consumer envelope error")
	}
	consumeErr := errors.New("consume failed")
	goodProducer := producerFunc(func(_ context.Context, eventType string) (events.Envelope, error) {
		return events.Envelope{EventType: eventType, Payload: contractObject(map[string]any{"id": "1"})}, nil
	})
	if err := VerifyConsumer(context.Background(), "order:created:v1:requested", goodProducer, consumerFunc(func(context.Context, events.Envelope) error {
		return consumeErr
	})); !errors.Is(err, consumeErr) {
		t.Fatalf("consumer error = %v", err)
	}
}
