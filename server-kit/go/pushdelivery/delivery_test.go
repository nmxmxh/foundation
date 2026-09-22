package pushdelivery

import (
	"context"
	"errors"
	"testing"
	"time"
)

type storeStub struct {
	items                 []Delivery
	claimed               int
	completed             []Result
	claimErr, completeErr error
	limit                 int
}

func (s *storeStub) Claim(ctx context.Context, limit int) ([]Delivery, error) {
	s.claimed++
	s.limit = limit
	return s.items, s.claimErr
}
func (s *storeStub) Complete(ctx context.Context, d Delivery, r Result) error {
	s.completed = append(s.completed, r)
	return s.completeErr
}

type senderFunc func(context.Context, Delivery) Result

func (f senderFunc) Send(c context.Context, d Delivery) Result { return f(c, d) }
func item() Delivery {
	return Delivery{ID: "delivery", TenantID: "org", RecipientID: "owner", CorrelationID: "trace", Attempt: 1, Payload: []byte("notice")}
}
func TestConfiguration(t *testing.T) {
	for _, modify := range []func(*Options){
		func(o *Options) { o.BatchSize = 0 }, func(o *Options) { o.BatchSize = 65 }, func(o *Options) { o.AttemptTimeout = 0 },
		func(o *Options) { o.AttemptTimeout = time.Minute }, func(o *Options) { o.StoreTimeout = 0 }, func(o *Options) { o.StoreTimeout = time.Minute },
		func(o *Options) { o.IdleMin = 0 }, func(o *Options) { o.IdleMax = 0 }, func(o *Options) { o.IdleMax = time.Hour },
	} {
		o := DefaultOptions()
		modify(&o)
		if _, err := New(&storeStub{}, senderFunc(nil), o); err == nil {
			t.Fatal("accepted invalid options")
		}
	}
	if _, err := New(nil, senderFunc(nil), DefaultOptions()); err == nil {
		t.Fatal("accepted nil store")
	}
	if _, err := New(&storeStub{}, nil, DefaultOptions()); err == nil {
		t.Fatal("accepted nil sender")
	}
}
func TestOnceAndBounds(t *testing.T) {
	s := &storeStub{items: []Delivery{item()}}
	observed := 0
	o := DefaultOptions()
	o.Observe = func(ctx context.Context, v Observation) {
		observed++
		if v.CorrelationID != "trace" {
			t.Fatal(v)
		}
	}
	r, _ := New(s, senderFunc(func(ctx context.Context, d Delivery) Result {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing deadline")
		}
		return Result{Outcome: Accepted}
	}), o)
	n, err := r.Once(context.Background())
	if err != nil || n != 1 || observed != 1 || s.limit != 8 {
		t.Fatal(n, err, observed)
	}
	s.claimErr = errors.New("offline")
	if _, err = r.Once(context.Background()); err == nil {
		t.Fatal("lost claim error")
	}
	s.claimErr = nil
	s.completeErr = errors.New("offline")
	if _, err = r.Once(context.Background()); err == nil {
		t.Fatal("lost completion error")
	}
	s.completeErr = nil
	s.items = make([]Delivery, 9)
	if _, err = r.Once(context.Background()); err == nil {
		t.Fatal("accepted excessive batch")
	}
	s.items = []Delivery{item()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = r.Once(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestDeliveryPolicy(t *testing.T) {
	called := 0
	result := Result{Outcome: Retry}
	r, _ := New(&storeStub{}, senderFunc(func(context.Context, Delivery) Result { called++; return result }), DefaultOptions())
	for _, mutate := range []func(*Delivery){func(d *Delivery) { d.ID = "" }, func(d *Delivery) { d.TenantID = "" }, func(d *Delivery) { d.RecipientID = "" }, func(d *Delivery) { d.CorrelationID = "" }, func(d *Delivery) { d.Attempt = 0 }, func(d *Delivery) { d.Attempt = 6 }, func(d *Delivery) { d.Payload = make([]byte, 3001) }} {
		d := item()
		mutate(&d)
		if r.deliver(context.Background(), d).Outcome != Rejected {
			t.Fatal("accepted invalid delivery")
		}
	}
	if called != 0 {
		t.Fatal("sent invalid delivery")
	}
	if got := r.deliver(context.Background(), item()); got.Outcome != Retry || got.RetryAfter != 15*time.Second {
		t.Fatal(got)
	}
	d := item()
	d.Attempt = 5
	if got := r.deliver(context.Background(), d); got.Outcome != Rejected || got.RetryAfter != 5*time.Minute {
		t.Fatal(got)
	}
	result = Result{Outcome: Retry, RetryAfter: time.Hour}
	if got := r.deliver(context.Background(), item()); got.RetryAfter != 5*time.Minute {
		t.Fatal(got)
	}
	result = Result{Outcome: "unknown"}
	if r.deliver(context.Background(), item()).Outcome != Rejected {
		t.Fatal("unknown outcome")
	}
}
func TestRunWakeCancellationAndErrors(t *testing.T) {
	for _, failure := range []bool{false, true} {
		s := &storeStub{}
		if failure {
			s.claimErr = errors.New("offline")
		}
		o := DefaultOptions()
		o.IdleMin = time.Millisecond
		o.IdleMax = 2 * time.Millisecond
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		o.Observe = func(context.Context, Observation) { cancel() }
		if !failure {
			s.items = []Delivery{item()}
		}
		r, _ := New(s, senderFunc(func(context.Context, Delivery) Result { return Result{Outcome: Accepted} }), o)
		wake := make(chan struct{})
		close(wake)
		err := r.Run(ctx, wake, func(error) { calls++; cancel() })
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if failure && calls != 1 {
			t.Fatal(calls)
		}
	}
	// A closed wake channel must not cause a busy loop.
	s := &storeStub{}
	o := DefaultOptions()
	o.IdleMin = time.Millisecond
	o.IdleMax = 4 * time.Millisecond
	r, _ := New(s, senderFunc(nil), o)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	wake := make(chan struct{})
	close(wake)
	_ = r.Run(ctx, wake, nil)
	if s.claimed > 15 || s.claimed < 2 {
		t.Fatalf("idle claims: %d", s.claimed)
	}
}
func BenchmarkEmptyBatch(b *testing.B) {
	r, _ := New(&storeStub{}, senderFunc(nil), DefaultOptions())
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = r.Once(ctx)
	}
}
