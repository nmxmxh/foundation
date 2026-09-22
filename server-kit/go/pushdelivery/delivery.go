// Package pushdelivery provides bounded delivery for durable notification outboxes.
package pushdelivery

import (
	"context"
	"errors"
	"time"
)

type Outcome string

const (
	Accepted Outcome = "accepted"
	Retry    Outcome = "retry"
	Expired  Outcome = "expired"
	Rejected Outcome = "rejected"
)

// Destination contains private transport credentials. Never log this value.
type Destination struct{ Endpoint, PublicKey, AuthSecret string }
type Delivery struct {
	ID, TenantID, RecipientID, CorrelationID string
	Attempt                                  int
	Destination                              Destination
	Payload                                  []byte
}
type Result struct {
	Outcome    Outcome
	StatusCode int
	RetryAfter time.Duration
}
type Sender interface {
	Send(context.Context, Delivery) Result
}

// Store must atomically lease records and enforce recipient scope before delivery.
// Complete must match the lease attempt and preserve retry state after process failure.
type Store interface {
	Claim(context.Context, int) ([]Delivery, error)
	Complete(context.Context, Delivery, Result) error
}

// Observation excludes endpoints, payloads, and provider credentials.
type Observation struct {
	ID, TenantID, CorrelationID string
	Attempt                     int
	Result                      Result
}
type Options struct {
	BatchSize                                      int
	AttemptTimeout, StoreTimeout, IdleMin, IdleMax time.Duration
	Observe                                        func(context.Context, Observation)
}

func DefaultOptions() Options {
	return Options{BatchSize: 8, AttemptTimeout: 8 * time.Second, StoreTimeout: 5 * time.Second,
		IdleMin: 250 * time.Millisecond, IdleMax: 30 * time.Second}
}

type Runner struct {
	store   Store
	sender  Sender
	options Options
}

func New(store Store, sender Sender, options Options) (*Runner, error) {
	if store == nil || sender == nil || options.BatchSize < 1 || options.BatchSize > 64 ||
		options.AttemptTimeout <= 0 || options.AttemptTimeout > 30*time.Second ||
		options.StoreTimeout <= 0 || options.StoreTimeout > 10*time.Second ||
		options.IdleMin < time.Millisecond || options.IdleMax < options.IdleMin || options.IdleMax > 5*time.Minute {
		return nil, errors.New("invalid push delivery configuration")
	}
	return &Runner{store, sender, options}, nil
}

// Once processes one batch. The caller owns process lifetime and wakeup signals.
func (r *Runner) Once(ctx context.Context) (int, error) {
	claimCtx, cancel := context.WithTimeout(ctx, r.options.StoreTimeout)
	items, err := r.store.Claim(claimCtx, r.options.BatchSize)
	cancel()
	if err != nil {
		return 0, err
	}
	if len(items) > r.options.BatchSize {
		return 0, errors.New("push store exceeded batch limit")
	}
	for i, d := range items {
		if err := ctx.Err(); err != nil {
			return i, err
		}
		result := r.deliver(ctx, d)
		completeCtx, completeCancel := context.WithTimeout(ctx, r.options.StoreTimeout)
		err := r.store.Complete(completeCtx, d, result)
		completeCancel()
		if err != nil {
			return i, err
		}
		if r.options.Observe != nil {
			r.options.Observe(ctx, Observation{d.ID, d.TenantID, d.CorrelationID, d.Attempt, result})
		}
	}
	return len(items), nil
}

func (r *Runner) deliver(ctx context.Context, d Delivery) Result {
	if d.ID == "" || d.TenantID == "" || d.RecipientID == "" || d.CorrelationID == "" || d.Attempt < 1 || d.Attempt > 5 || len(d.Payload) > 3000 {
		return Result{Outcome: Rejected}
	}
	attemptCtx, cancel := context.WithTimeout(ctx, r.options.AttemptTimeout)
	defer cancel()
	result := r.sender.Send(attemptCtx, d)
	switch result.Outcome {
	case Accepted, Rejected, Expired:
	case Retry:
		if d.Attempt >= 5 {
			result.Outcome = Rejected
		}
		if result.RetryAfter <= 0 {
			result.RetryAfter = time.Duration(15*d.Attempt*d.Attempt) * time.Second
		}
		result.RetryAfter = min(result.RetryAfter, 5*time.Minute)
	default:
		result = Result{Outcome: Rejected}
	}
	return result
}

// Run sleeps between batches. Signals coalesce; idle stores receive progressively fewer reads.
// Cancellation bounds this service loop. Store errors use the maximum idle delay.
func (r *Runner) Run(ctx context.Context, wake <-chan struct{}, report func(error)) error {
	delay := r.options.IdleMin
	for ctx.Err() == nil {
		count, err := r.Once(ctx)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			delay = r.options.IdleMax
			if report != nil {
				report(err)
			}
		} else if count > 0 {
			delay = r.options.IdleMin
		} else {
			delay = min(delay*2, r.options.IdleMax)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
		case _, ok := <-wake:
			if !ok {
				wake = nil
			}
		case <-timer.C:
		}
		timer.Stop()
	}
	return ctx.Err()
}
