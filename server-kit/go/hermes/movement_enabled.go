//go:build foundation_movement

package hermes

import "sync/atomic"

type movementCounts [movementCounterCount]atomic.Uint64

// Install one collector for each serial diagnostic interval.
var activeMovement atomic.Pointer[movementCounts]

func movementRecord(counter movementCounter, amount int) {
	if amount <= 0 {
		return
	}
	if counts := activeMovement.Load(); counts != nil {
		counts[counter].Add(uint64(amount))
	}
}
