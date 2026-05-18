package chain

import (
	"context"
	"errors"
	"sync"
)

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
			result.Error = errors.New("operation run function is nil")
			return []Result[T]{result}
		}
		result.Value, result.Error = operations[0].Run(ctx)
		return []Result[T]{result}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]Result[T], len(operations))
	var wg sync.WaitGroup
	for index, operation := range operations {
		results[index].Name = operation.Name
		wg.Add(1)
		go runOperation(ctx, &wg, cancel, results, index, operation)
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
			results[0].Error = errors.New("operation run function is nil")
			return results
		}
		results[0].Value, results[0].Error = operations[0].Run(ctx)
		return results
	}
	ctx, cancel := context.WithCancel(ctx)

	var wg sync.WaitGroup
	for index, operation := range operations {
		wg.Add(1)
		go runOperation(ctx, &wg, cancel, results, index, operation)
	}
	wg.Wait()
	cancel()
	return results
}

func runOperation[T any](ctx context.Context, wg *sync.WaitGroup, cancel context.CancelFunc, results []Result[T], index int, operation Operation[T]) {
	defer wg.Done()
	if operation.Run == nil {
		results[index].Error = errors.New("operation run function is nil")
		if operation.Critical {
			cancel()
		}
		return
	}
	value, err := operation.Run(ctx)
	results[index].Value = value
	results[index].Error = err
	if err != nil && operation.Critical {
		cancel()
	}
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
