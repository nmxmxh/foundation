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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]Result[T], len(operations))
	var wg sync.WaitGroup
	for index, operation := range operations {
		results[index].Name = operation.Name
		wg.Add(1)
		go func() {
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
		}()
	}
	wg.Wait()
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
