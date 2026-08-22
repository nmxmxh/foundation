//go:build !(linux || darwin)

package runtimehost

import "errors"

// Dispatch placement requires a shared-memory mapping, which this platform
// does not provide. The pure decision rules in dispatch.go remain available;
// only the region-backed half is absent.

var errDispatchUnsupported = errors.New("dispatch regions require linux or darwin")

func OpenDispatchRegion(string) (*DispatchBlock, error) { return nil, errDispatchUnsupported }

func (b *DispatchBlock) Close() error { return nil }

func (b *DispatchBlock) FlipIndex() (uint32, error) { return 0, errDispatchUnsupported }

func (b *DispatchBlock) TickNow() (uint64, error) { return 0, errDispatchUnsupported }

func (b *DispatchBlock) AdvanceTick() (uint64, error) { return 0, errDispatchUnsupported }

func (b *DispatchBlock) StatRow(int) (*DispatchStatRow, error) {
	return nil, errDispatchUnsupported
}

func (s *DispatchStatRow) Claim() (uint32, error) { return 0, errDispatchUnsupported }

func (s *DispatchStatRow) ReleaseOne() (bool, error) { return false, errDispatchUnsupported }

func (s *DispatchStatRow) RecordCompletion(uint64, uint64) (uint64, error) {
	return 0, errDispatchUnsupported
}

func (s *DispatchStatRow) Heartbeat(uint64) error { return errDispatchUnsupported }

func (s *DispatchStatRow) ApplyMirror(DispatchLaneStats) error { return errDispatchUnsupported }

func (s *DispatchStatRow) Snapshot() (DispatchLaneStats, error) {
	return DispatchLaneStats{}, errDispatchUnsupported
}

func (b *DispatchBlock) SnapshotStatRow(int) (DispatchLaneStats, error) {
	return DispatchLaneStats{}, errDispatchUnsupported
}

func (b *DispatchBlock) PublishDescriptors([]DispatchLaneDescriptor, uint32) (uint32, error) {
	return 0, errDispatchUnsupported
}

func (b *DispatchBlock) SnapshotDescriptors() ([]DispatchLaneDescriptor, error) {
	return nil, errDispatchUnsupported
}
