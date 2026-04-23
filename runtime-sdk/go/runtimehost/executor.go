package runtimehost

import "context"

// Executor is the transport-agnostic runtime execution boundary.
type Executor interface {
	Execute(context.Context, ProcessRequest) (ProcessResponse, error)
	Close() error
}
