//go:build foundation_movement

package runtimehost

import "testing"

func TestMovementCollectorOwnership(t *testing.T) {
	first, ok := startMovementCollection()
	if !ok {
		t.Fatal("initial collection refused")
	}
	defer first.stop()
	second, ok := startMovementCollection()
	if ok || second.stop() {
		t.Fatal("overlapping collection accepted")
	}
	movementRecordInputCopy(3)
	movementRecordOutputCopy(5)
	movementRecordControlClear(7)
	movementRecordErrorClear(11)
	if got := first.snapshot(); got != (movementSnapshot{3, 5, 7, 11}) {
		t.Fatalf("snapshot: %+v", got)
	}
	if !first.stop() || first.stop() {
		t.Fatal("collector release is inconsistent")
	}
	movementRecordInputCopy(13)
	movementRecordOutputCopy(17)
	movementRecordControlClear(19)
	movementRecordErrorClear(23)
	if got := first.snapshot(); got != (movementSnapshot{3, 5, 7, 11}) {
		t.Fatalf("inactive collection changed: %+v", got)
	}
}
