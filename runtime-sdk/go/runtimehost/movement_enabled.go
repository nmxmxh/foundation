//go:build foundation_movement

package runtimehost

import "sync/atomic"

// Movement probes cover selected Go host operations, excluding native code and hardware transfers.
// Collectors require an isolated diagnostic process without unrelated runtime traffic.
type movementCollector struct {
	inputCopied    atomic.Uint64
	outputCopied   atomic.Uint64
	controlCleared atomic.Uint64
	errorCleared   atomic.Uint64
}

type movementSnapshot struct {
	inputCopied, outputCopied, controlCleared, errorCleared uint64
}

var activeMovementCollector atomic.Pointer[movementCollector]

func startMovementCollection() (*movementCollector, bool) {
	collector := new(movementCollector)
	return collector, activeMovementCollector.CompareAndSwap(nil, collector)
}

func (c *movementCollector) stop() bool {
	return activeMovementCollector.CompareAndSwap(c, nil)
}

func (c *movementCollector) snapshot() movementSnapshot {
	return movementSnapshot{c.inputCopied.Load(), c.outputCopied.Load(), c.controlCleared.Load(), c.errorCleared.Load()}
}

func movementRecordInputCopy(copied int) {
	if c := activeMovementCollector.Load(); c != nil {
		c.inputCopied.Add(uint64(copied))
	}
}

func movementRecordOutputCopy(copied int) {
	if c := activeMovementCollector.Load(); c != nil {
		c.outputCopied.Add(uint64(copied))
	}
}

func movementRecordControlClear(cleared int) {
	if c := activeMovementCollector.Load(); c != nil {
		c.controlCleared.Add(uint64(cleared))
	}
}

func movementRecordErrorClear(cleared int) {
	if c := activeMovementCollector.Load(); c != nil {
		c.errorCleared.Add(uint64(cleared))
	}
}
