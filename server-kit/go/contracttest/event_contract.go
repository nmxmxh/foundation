package contracttest

import (
	"context"
	"fmt"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
)

type Producer interface {
	ProduceContractEvent(context.Context, string) (events.Envelope, error)
}

type Consumer interface {
	ConsumeContractEvent(context.Context, events.Envelope) error
}

func VerifyProducer(ctx context.Context, eventType string, producer Producer) error {
	if producer == nil {
		return fmt.Errorf("producer is nil")
	}
	env, err := producer.ProduceContractEvent(ctx, eventType)
	if err != nil {
		return err
	}
	if env.EventType != eventType {
		return fmt.Errorf("producer emitted %q, want %q", env.EventType, eventType)
	}
	env.Normalize()
	return env.Validate()
}

func VerifyConsumer(ctx context.Context, eventType string, producer Producer, consumer Consumer) error {
	if consumer == nil {
		return fmt.Errorf("consumer is nil")
	}
	env, err := producer.ProduceContractEvent(ctx, eventType)
	if err != nil {
		return err
	}
	if err := events.ValidateEventType(env.EventType); err != nil {
		return err
	}
	env.Normalize()
	if err := env.Validate(); err != nil {
		return err
	}
	return consumer.ConsumeContractEvent(ctx, env)
}
