package placement

import (
	"bytes"
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

func bindingContext(tb testing.TB) context.Context {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	tb.Cleanup(cancel)
	ctx = security.ContextWithOrganizationID(ctx, "tenant-a")
	md := metadata.New()
	md.CorrelationID = "binding-test"
	return metadata.IntoContext(ctx, md)
}

func bindingFixture(tb testing.TB, operation BindingOperation) (*BindingRegistry, context.Context, RuntimeBindingRequest, BindingSpec) {
	tb.Helper()
	ctx := bindingContext(tb)
	validate := func(data []byte) error {
		if len(data) != 8 {
			return ErrBindingInvalid
		}
		return nil
	}
	r, err := NewBindingRegistry(11, BindingLimits{8, 8, 1024, 4},
		func(context.Context, BindingAction, uint64, uint64) error { return nil }, BindingType{101, validate})
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := r.PublishResource(ctx, 7, 101, 0, []byte("resident")); err != nil {
		tb.Fatal(err)
	}
	if operation == nil {
		operation = func(_ context.Context, input, output []byte) (uint32, error) { copy(output, input); return 8, nil }
	}
	spec := BindingSpec{InputCopiesKnown: true, ID: 9, ResourceID: 7, ResourceGeneration: 1, InputTypeID: 101, OutputTypeID: 101,
		MaxInputBytes: 8, MaxOutputBytes: 8, MaxConcurrency: 2, Operation: operation}
	request, err := r.Bind(ctx, spec, 0)
	if err != nil {
		tb.Fatal(err)
	}
	return r, ctx, request, spec
}

func TestBindingResidentReplacement(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	r, ctx, request, spec := bindingFixture(t, func(ctx context.Context, input, output []byte) (uint32, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		copy(output, input)
		return 8, nil
	})
	result := make(chan error, 1)
	go func() {
		var output [8]byte
		_, err := r.Execute(ctx, request, "inflight", output[:])
		if err == nil && string(output[:]) != "resident" {
			err = errors.New("inflight generation changed")
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := r.Close(); !errors.Is(err, ErrBindingBusy) {
		t.Fatalf("close active: %v", err)
	}
	data := []byte("replaced")
	if _, err := r.PublishResource(ctx, 7, 101, 1, data); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	if got := r.Stats(); got.ResidentBytes != 16 || got.InFlight != 1 {
		t.Fatalf("retirement budget: %+v", got)
	}
	var output [8]byte
	if _, err := r.Execute(ctx, request, "stale", output[:]); !errors.Is(err, ErrBindingStale) {
		t.Fatalf("stale: %v", err)
	}
	spec.ResourceGeneration = 2
	spec.Operation = func(_ context.Context, input, output []byte) (uint32, error) { copy(output, input); return 8, nil }
	next, err := r.Bind(ctx, spec, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(ctx, next, "new", output[:]); err != nil || string(output[:]) != "replaced" {
		t.Fatalf("new generation: %q %v", output, err)
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if got := r.Stats(); got.ResidentBytes != 8 || got.InFlight != 0 {
		t.Fatalf("retired release: %+v", got)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if got := r.Stats(); got != (BindingStats{}) {
		t.Fatalf("closed: %+v", got)
	}
	if _, err := r.Execute(ctx, next, "closed", output[:]); !errors.Is(err, ErrBindingBusy) {
		t.Fatal(err)
	}
	if _, err := r.PublishResource(ctx, 7, 101, 2, data); !errors.Is(err, ErrBindingBusy) {
		t.Fatal(err)
	}
	if _, err := r.Bind(ctx, spec, 2); !errors.Is(err, ErrBindingBusy) {
		t.Fatal(err)
	}
}

func TestBindingAuthorizationAndTypes(t *testing.T) {
	r, ctx, request, _ := bindingFixture(t, nil)
	var output [8]byte
	other := security.ContextWithOrganizationID(ctx, "tenant-b")
	if _, err := r.Execute(other, request, "tenant", output[:]); !errors.Is(err, ErrBindingStale) {
		t.Fatalf("cross tenant: %v", err)
	}
	for _, tenant := range []string{"", string(bytes.Repeat([]byte{'x'}, 257))} {
		if _, err := r.PublishResource(security.ContextWithOrganizationID(ctx, tenant), 1, 101, 0, []byte("resident")); !errors.Is(err, ErrBindingForbidden) {
			t.Fatal(err)
		}
	}
	r.authorize = func(context.Context, BindingAction, uint64, uint64) error { return errors.New("policy denied") }
	if _, err := r.Execute(ctx, request, "denied", output[:]); !errors.Is(err, ErrBindingForbidden) {
		t.Fatal(err)
	}
	if _, err := r.Bind(ctx, BindingSpec{}, 0); !errors.Is(err, ErrBindingForbidden) {
		t.Fatal(err)
	}
	r.authorize = func(context.Context, BindingAction, uint64, uint64) error { return nil }
	for _, candidate := range []struct {
		id, typ, expected uint64
		data              []byte
		want              error
	}{
		{0, 101, 0, []byte("resident"), ErrBindingInvalid}, {8, 0, 0, []byte("resident"), ErrBindingInvalid},
		{8, 101, 0, nil, ErrBindingInvalid}, {8, 102, 0, []byte("resident"), ErrBindingInvalid},
		{8, 101, 0, []byte("bad"), ErrBindingInvalid}, {8, 101, 0, make([]byte, 1025), ErrBindingBudget},
		{8, 101, 1, []byte("resident"), ErrBindingStale}, {7, 101, 0, []byte("resident"), ErrBindingStale},
		{8, 101, math.MaxUint64, []byte("resident"), ErrBindingStale},
	} {
		if _, err := r.PublishResource(ctx, candidate.id, candidate.typ, candidate.expected, candidate.data); !errors.Is(err, candidate.want) {
			t.Fatalf("publish %+v: %v", candidate, err)
		}
	}
	missing := metadata.IntoContext(ctx, metadata.EnvelopeMetadata{})
	if _, err := r.PublishResource(missing, 8, 101, 0, []byte("resident")); !errors.Is(err, ErrBindingInvalid) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Execute(canceled, request, "cancel", output[:]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := validateBindingValue(func([]byte) error { panic("validator") }, output[:]); !errors.Is(err, ErrBindingExecution) {
		t.Fatal(err)
	}
}

func TestBindingValidation(t *testing.T) {
	r, ctx, request, spec := bindingFixture(t, nil)
	for _, mutate := range []func(*RuntimeBindingRequest){
		func(v *RuntimeBindingRequest) { v.SchemaVersion++ }, func(v *RuntimeBindingRequest) { v.RegistryID++ },
		func(v *RuntimeBindingRequest) { v.OutputCapacity = 0 }, func(v *RuntimeBindingRequest) { v.OutputCapacity = MaxBindingOutputBytes + 1 },
		func(v *RuntimeBindingRequest) { v.TimeoutMillis = 0 }, func(v *RuntimeBindingRequest) { v.TimeoutMillis = MaxBindingTimeoutMillis + 1 },
		func(v *RuntimeBindingRequest) { v.InputTypeID++ }, func(v *RuntimeBindingRequest) { v.OutputTypeID++ },
	} {
		v := request
		mutate(&v)
		if _, err := r.Execute(ctx, v, "invalid", make([]byte, 8)); !errors.Is(err, ErrBindingInvalid) {
			t.Fatalf("request %+v: %v", v, err)
		}
	}
	for _, correlation := range []string{"", string(bytes.Repeat([]byte{'x'}, 129))} {
		if _, err := r.Execute(ctx, request, correlation, make([]byte, 8)); !errors.Is(err, ErrBindingInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := r.Execute(context.Background(), request, "deadline", make([]byte, 8)); !errors.Is(err, ErrBindingInvalid) {
		t.Fatal(err)
	}
	short := request
	short.TimeoutMillis = 1
	if _, err := r.Execute(ctx, short, "deadline", make([]byte, 8)); !errors.Is(err, ErrBindingInvalid) {
		t.Fatal(err)
	}
	if _, err := r.Execute(ctx, request, "capacity", make([]byte, 7)); !errors.Is(err, ErrBindingInvalid) {
		t.Fatal(err)
	}
	large := request
	large.OutputCapacity = 9
	if _, err := r.Execute(ctx, large, "budget", make([]byte, 9)); !errors.Is(err, ErrBindingBudget) {
		t.Fatal(err)
	}
	for _, mutate := range []func(*BindingSpec){
		func(v *BindingSpec) { v.InputCopiesKnown = false },
		func(v *BindingSpec) { v.InputCopies = 17 },
		func(v *BindingSpec) { v.ID = 0 }, func(v *BindingSpec) { v.Operation = nil }, func(v *BindingSpec) { v.InputTypeID = 0 },
		func(v *BindingSpec) { v.OutputTypeID = 0 }, func(v *BindingSpec) { v.OutputTypeID = 102 }, func(v *BindingSpec) { v.MaxInputBytes = 0 },
		func(v *BindingSpec) { v.MaxOutputBytes = 0 }, func(v *BindingSpec) { v.MaxOutputBytes = MaxBindingOutputBytes + 1 },
		func(v *BindingSpec) { v.MaxConcurrency = 0 }, func(v *BindingSpec) { v.MaxConcurrency = 5 },
		func(v *BindingSpec) { v.InputTypeID = 102 }, func(v *BindingSpec) { v.MaxInputBytes = 7 },
	} {
		v := spec
		mutate(&v)
		if _, err := r.Bind(ctx, v, 1); !errors.Is(err, ErrBindingInvalid) {
			t.Fatalf("spec %+v: %v", v, err)
		}
	}
	for _, expected := range []uint64{0, 2, math.MaxUint64} {
		if _, err := r.Bind(ctx, spec, expected); !errors.Is(err, ErrBindingStale) {
			t.Fatal(err)
		}
	}
	spec.ID = 10
	if _, err := r.Bind(ctx, spec, 1); !errors.Is(err, ErrBindingStale) {
		t.Fatal(err)
	}
	spec.ResourceGeneration++
	if _, err := r.Bind(ctx, spec, 0); !errors.Is(err, ErrBindingStale) {
		t.Fatal(err)
	}
}

func TestBindingLimits(t *testing.T) {
	r, ctx, request, spec := bindingFixture(t, nil)
	r.limits.Resources = 1
	r.limits.Bindings = 1
	r.limits.ResidentBytes = 8
	if _, err := r.PublishResource(ctx, 8, 101, 0, []byte("resident")); !errors.Is(err, ErrBindingBudget) {
		t.Fatal(err)
	}
	if _, err := r.PublishResource(ctx, 7, 101, 1, []byte("resident")); !errors.Is(err, ErrBindingBudget) {
		t.Fatal(err)
	}
	spec.ID = 10
	if _, err := r.Bind(ctx, spec, 0); !errors.Is(err, ErrBindingBudget) {
		t.Fatal(err)
	}
	resource, slot, _, err := r.acquire("tenant-a", request)
	if err != nil {
		t.Fatal(err)
	}
	defer r.finish(resource, slot)
	resource2, slot2, _, err := r.acquire("tenant-a", request)
	if err != nil {
		t.Fatal(err)
	}
	defer r.finish(resource2, slot2)
	if _, _, _, err := r.acquire("tenant-a", request); !errors.Is(err, ErrBindingBusy) {
		t.Fatal(err)
	}
	r.limits.InFlight = 2
	if _, _, _, err := r.acquire("tenant-a", request); !errors.Is(err, ErrBindingBusy) {
		t.Fatal(err)
	}
}

func TestBindingOperationFailures(t *testing.T) {
	for _, candidate := range []struct {
		operation BindingOperation
		want      error
	}{
		{func(context.Context, []byte, []byte) (uint32, error) { return 0, ErrBindingBusy }, ErrBindingBusy},
		{func(context.Context, []byte, []byte) (uint32, error) { panic("native defect") }, ErrBindingExecution},
		{func(context.Context, []byte, []byte) (uint32, error) { return 9, nil }, ErrBindingBudget},
		{func(context.Context, []byte, []byte) (uint32, error) { return 7, nil }, ErrBindingInvalid},
	} {
		r, ctx, request, _ := bindingFixture(t, candidate.operation)
		if _, err := r.Execute(ctx, request, "failure", make([]byte, 8)); !errors.Is(err, candidate.want) {
			t.Fatal(err)
		}
		if r.Stats().InFlight != 0 {
			t.Fatal("execution lease leaked")
		}
	}
	r, ctx, request, spec := bindingFixture(t, nil)
	canceled, cancel := context.WithCancel(ctx)
	spec.Operation = func(context.Context, []byte, []byte) (uint32, error) { cancel(); return 8, nil }
	request, err := r.Bind(ctx, spec, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(canceled, request, "cancel", make([]byte, 8)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBindingCopyBudgetAndCorrelation(t *testing.T) {
	r, ctx, request, spec := bindingFixture(t, nil)
	calls := 0
	spec.InputCopies = 1
	spec.Operation = func(ctx context.Context, input, output []byte) (uint32, error) {
		if metadata.FromContext(ctx).CorrelationID != "actual-execution" {
			return 0, ErrBindingInvalid
		}
		calls++
		copy(output, input)
		return 8, nil
	}
	request, err := r.Bind(ctx, spec, 1)
	if err != nil {
		t.Fatal(err)
	}
	var output [8]byte
	request.MaxInputCopyBytes = 0
	if _, err := r.Execute(ctx, request, "actual-execution", output[:]); !errors.Is(err, ErrBindingBudget) || calls != 0 {
		t.Fatalf("copy requirement bypassed: %v calls=%d", err, calls)
	}
	request.MaxInputCopyBytes = 8
	receipt, err := r.Execute(ctx, request, "actual-execution", output[:])
	if err != nil || receipt.InputCopiedBytes != 8 || calls != 1 {
		t.Fatalf("copy receipt: %+v %v", receipt, err)
	}
}

func TestBindingConcurrentPublication(t *testing.T) {
	r, ctx, _, _ := bindingFixture(t, nil)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.PublishResource(ctx, 7, 101, 1, []byte("new-data"))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	won := 0
	for err := range results {
		if err == nil {
			won++
		} else if !errors.Is(err, ErrBindingStale) {
			t.Fatal(err)
		}
	}
	if won != 1 {
		t.Fatalf("CAS winners: %d", won)
	}
}

func TestBindingZeroAllocDispatch(t *testing.T) {
	r, ctx, request, _ := bindingFixture(t, nil)
	var output [8]byte
	allocs := testing.AllocsPerRun(1000, func() {
		receipt, err := r.Execute(ctx, request, "binding-test", output[:])
		if err != nil || receipt.InputCopiedBytes != 0 || receipt.TransferredBytes != 0 {
			panic("invalid receipt")
		}
	})
	if allocs != 0 {
		t.Fatalf("resident dispatch allocated: %f", allocs)
	}
}

func BenchmarkBindingResident(b *testing.B) {
	r, ctx, request, spec := bindingFixture(b, nil)
	var output [8]byte
	input := []byte("resident")
	b.Run("direct", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := spec.Operation(ctx, input, output[:]); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("checked", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := r.Execute(ctx, request, "binding-test", output[:]); err != nil {
				b.Fatal(err)
			}
		}
	})
}
