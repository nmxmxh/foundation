package chain

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var ErrNilOperationRun = errors.New("operation run function is nil")

type Operation[T any] struct {
	Name     string
	Critical bool
	Run      func(context.Context) (T, error)
}

type Result[T any] struct {
	Name  string
	Value T
	Error error
}

func RunParallel[T any](ctx context.Context, operations []Operation[T]) []Result[T] {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(operations) == 0 {
		return nil
	}
	if len(operations) == 1 {
		result := Result[T]{Name: operations[0].Name}
		if operations[0].Run == nil {
			result.Error = ErrNilOperationRun
			return []Result[T]{result}
		}
		result.Value, result.Error = operations[0].Run(ctx)
		return []Result[T]{result}
	}
	cancelCtx := newChainContext(ctx)
	defer cancelCtx.Cancel()

	results := make([]Result[T], len(operations))
	var wg sync.WaitGroup
	for index, operation := range operations {
		results[index].Name = operation.Name
		wg.Add(1)
		go runOperation(cancelCtx, &wg, cancelCtx, results, index, operation)
	}
	wg.Wait()
	return results
}

// RunParallelInto runs operations concurrently and writes results into caller-owned
// storage when it has enough capacity. The returned slice is always scoped to the
// operation count. Callers must not share the result slice concurrently.
func RunParallelInto[T any](ctx context.Context, operations []Operation[T], results []Result[T]) []Result[T] {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(operations) == 0 {
		return results[:0]
	}
	results = prepareResults(operations, results)
	if len(operations) == 1 {
		if operations[0].Run == nil {
			results[0].Error = ErrNilOperationRun
			return results
		}
		results[0].Value, results[0].Error = operations[0].Run(ctx)
		return results
	}
	cancelCtx := newChainContext(ctx)

	var wg sync.WaitGroup
	for index, operation := range operations {
		wg.Add(1)
		go runOperation(cancelCtx, &wg, cancelCtx, results, index, operation)
	}
	wg.Wait()
	cancelCtx.Cancel()
	return results
}

type operationCanceler interface {
	Cancel()
}

func runOperation[T any](ctx context.Context, wg *sync.WaitGroup, cancel operationCanceler, results []Result[T], index int, operation Operation[T]) {
	defer wg.Done()
	if operation.Run == nil {
		results[index].Error = ErrNilOperationRun
		if operation.Critical {
			cancel.Cancel()
		}
		return
	}
	value, err := operation.Run(ctx)
	results[index].Value = value
	results[index].Error = err
	if err != nil && operation.Critical {
		cancel.Cancel()
	}
}

type chainContext struct {
	parent    context.Context
	done      atomic.Pointer[chan struct{}]
	closed    atomic.Bool
	once      sync.Once
	watchOnce sync.Once
}

func newChainContext(parent context.Context) *chainContext {
	return &chainContext{parent: parent}
}

func (c *chainContext) Deadline() (time.Time, bool) {
	return c.parent.Deadline()
}

func (c *chainContext) Done() <-chan struct{} {
	if c.closed.Load() {
		closed := closedChainDone()
		return closed
	}
	ch := c.ensureDone()
	c.watchParent()
	return ch
}

func (c *chainContext) Err() error {
	if c.closed.Load() {
		return context.Canceled
	}
	return c.parent.Err()
}

func (c *chainContext) Value(key any) any {
	return c.parent.Value(key)
}

func (c *chainContext) Cancel() {
	c.once.Do(func() {
		c.closed.Store(true)
		if done := c.done.Load(); done != nil {
			close(*done)
		}
	})
}

func (c *chainContext) ensureDone() <-chan struct{} {
	if done := c.done.Load(); done != nil {
		return *done
	}
	ch := make(chan struct{})
	if c.done.CompareAndSwap(nil, &ch) {
		return ch
	}
	return *c.done.Load()
}

func (c *chainContext) watchParent() {
	parentDone := c.parent.Done()
	if parentDone == nil {
		return
	}
	c.watchOnce.Do(func() {
		go func() {
			select {
			case <-parentDone:
				c.Cancel()
			case <-c.ensureDone():
			}
		}()
	})
}

var globalClosedChainDone chan struct{}
var globalClosedChainDoneOnce sync.Once

func closedChainDone() <-chan struct{} {
	globalClosedChainDoneOnce.Do(func() {
		globalClosedChainDone = make(chan struct{})
		close(globalClosedChainDone)
	})
	return globalClosedChainDone
}

func prepareResults[T any](operations []Operation[T], results []Result[T]) []Result[T] {
	if cap(results) < len(operations) {
		results = make([]Result[T], len(operations))
	} else {
		results = results[:len(operations)]
	}
	for index, operation := range operations {
		results[index] = Result[T]{Name: operation.Name}
	}
	return results
}

// HasCriticalFailureOrdered checks results returned by RunParallel or
// RunParallelInto without building a name lookup. It is the hot-path helper when
// callers keep operation/result order intact.
func HasCriticalFailureOrdered[T any](operations []Operation[T], results []Result[T]) bool {
	limit := min(len(results), len(operations))
	for index := range limit {
		if operations[index].Critical && results[index].Error != nil {
			return true
		}
	}
	return false
}

// HasCriticalFailure checks by operation name, which is useful when results have
// been filtered, merged, or reordered. Prefer HasCriticalFailureOrdered for
// direct RunParallel/RunParallelInto results.
func HasCriticalFailure[T any](operations []Operation[T], results []Result[T]) bool {
	critical := map[string]bool{}
	for _, operation := range operations {
		critical[operation.Name] = operation.Critical
	}
	for _, result := range results {
		if result.Error != nil && critical[result.Name] {
			return true
		}
	}
	return false
}
