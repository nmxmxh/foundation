//go:build !linux && !darwin

package runtimehost

import (
	"context"
	"errors"
	"time"
)

type epochWaitPolicy struct {
	spinIterations int
	maxSleep       time.Duration
	timeout        time.Duration
}

type epochExchange struct {
	shm    *sharedMemorySegment
	policy epochWaitPolicy
	alive  func() bool
}

func (x epochExchange) Exchange(_ context.Context, _ string, _ []byte) error {
	return errors.New("shared memory epoch exchange requires linux or darwin")
}

func (x epochExchange) Close() error {
	return nil
}

func (x epochExchange) Restart() error {
	return nil
}

func (t EpochWaitTuning) policy(timeout time.Duration) epochWaitPolicy {
	return defaultEpochWaitPolicy(timeout)
}

func defaultEpochWaitPolicy(timeout time.Duration) epochWaitPolicy {
	return epochWaitPolicy{
		spinIterations: 20000,
		maxSleep:       time.Millisecond,
		timeout:        timeout,
	}
}

func waitForKernelReady(_ []byte, _ epochWaitPolicy, _ func() bool) error {
	return errors.New("shared memory epoch exchange requires linux or darwin")
}
