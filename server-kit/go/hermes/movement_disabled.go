//go:build !foundation_movement

package hermes

// Remove diagnostic probes from ordinary builds through inlining.
func movementRecord(movementCounter, int) {}
