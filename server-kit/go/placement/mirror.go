// Package placement carries compute-placement signals between nodes.
//
// The dispatch lane table (ovrt-dispatch / runtimehost DispatchBlock) is the
// authoritative local view of executor health. This package moves that view
// between nodes over the existing transport bus, so a hub or peer can hold a
// mirrored picture of remote capacity without polling it.
package placement

import (
	"encoding/binary"
	"fmt"
)

// MirrorWireVersion guards the binary frame below. Bump when fields change;
// listeners refuse mismatched versions rather than misreading bytes.
const MirrorWireVersion = 1

// mirrorRecordBytes is the fixed per-lane record size:
// 2+2+4+4+4+1+8+8+8+8.
const mirrorRecordBytes = 49

// LaneClass marks a node's role in the mesh topology.
//
// Seeds hold authoritative tables and publish every class they run. Hubs own
// their region's table, aggregate their edges' updates, and publish upward
// and sideways. Edge nodes publish only what they sample, on change, and are
// the primary consumers of mirrors from their hub.
type LaneClass uint8

const (
	LaneClassEdge LaneClass = iota
	LaneClassHub
	LaneClassSeed
)

// LaneMirrorUpdate is one lane's reported state crossing the bus.
type LaneMirrorUpdate struct {
	Lane           uint16
	Jurisdiction   uint16
	MaxConcurrency uint32
	Inflight       uint32
	Generation     uint32
	Class          LaneClass
	UnitClassMask  uint64
	AffinityBloom  uint64
	EwmaNs         uint64
	TickSeen       uint64
}

// MinPlausibleEwmaNs is the floor for claimed completion latency on inbound
// mirrors.
//
// A lane reporting a mean below this claims to beat the dispatch decision
// itself at doing real work; that is a lie about physics, not speed. Zero is
// exempt: it truthfully means "unsampled". The floor caps what a lying
// publisher can win through argmin bias while first-party sampling corrects
// the picture.
const MinPlausibleEwmaNs = 100

// ValidateMirrorStats refuses mirror updates that could poison placement:
// absurd latency claims are rejected outright so operators see them through
// listener error callbacks instead of losing traffic silently.
func ValidateMirrorStats(update LaneMirrorUpdate) error {
	if update.EwmaNs != 0 && update.EwmaNs < MinPlausibleEwmaNs {
		return fmt.Errorf(
			"mirror lane %d claims %dns mean; below the %dns plausibility floor",
			update.Lane, update.EwmaNs, MinPlausibleEwmaNs,
		)
	}
	return nil
}

// EncodeLaneMirrorFrame packs node identity plus a batch of lane updates.
//
// Layout (little-endian): version byte, class byte, u16 node-id length +
// bytes, u16 region-id length + bytes, u16 lane count, then per lane one
// fixed mirrorRecordBytes record in field order above minus lengths. Fixed records keep
// the decoder bounds-checkable in one pass; JSON never touches this path
// because it rides the internal hot lane.
func EncodeLaneMirrorFrame(nodeID, regionID string, class LaneClass, lanes []LaneMirrorUpdate) ([]byte, error) {
	if len(lanes) == 0 {
		return nil, fmt.Errorf("mirror frame requires at least one lane update")
	}
	if len(lanes) > 255 {
		return nil, fmt.Errorf("mirror frame carries %d lanes; 255 maximum", len(lanes))
	}
	nodeBytes := []byte(nodeID)
	regionBytes := []byte(regionID)
	if len(nodeBytes) == 0 || len(nodeBytes) > 255 {
		return nil, fmt.Errorf("node id must be 1..255 bytes")
	}
	if len(regionBytes) == 0 || len(regionBytes) > 255 {
		return nil, fmt.Errorf("region id must be 1..255 bytes")
	}

	frame := make([]byte, 0, 7+len(nodeBytes)+len(regionBytes)+len(lanes)*mirrorRecordBytes)
	frame = append(frame, MirrorWireVersion, byte(class), byte(len(nodeBytes))) // #nosec G115 -- nodeBytes length validated 1..255 above
	frame = append(frame, nodeBytes...)
	frame = append(frame, byte(len(regionBytes))) // #nosec G115 -- regionBytes length validated 1..255 above
	frame = append(frame, regionBytes...)
	frame = binary.LittleEndian.AppendUint16(frame, uint16(len(lanes))) // #nosec G115 -- lane count validated <= 255 above

	for _, lane := range lanes {
		start := len(frame)
		frame = binary.LittleEndian.AppendUint16(frame, lane.Lane)
		frame = binary.LittleEndian.AppendUint16(frame, lane.Jurisdiction)
		frame = binary.LittleEndian.AppendUint32(frame, lane.MaxConcurrency)
		frame = binary.LittleEndian.AppendUint32(frame, lane.Inflight)
		frame = binary.LittleEndian.AppendUint32(frame, lane.Generation)
		frame = append(frame, byte(lane.Class))
		frame = binary.LittleEndian.AppendUint64(frame, lane.UnitClassMask)
		frame = binary.LittleEndian.AppendUint64(frame, lane.AffinityBloom)
		frame = binary.LittleEndian.AppendUint64(frame, lane.EwmaNs)
		frame = binary.LittleEndian.AppendUint64(frame, lane.TickSeen)
		if len(frame)-start != mirrorRecordBytes {
			return nil, fmt.Errorf("mirror record encoding drifted to %d bytes", len(frame)-start)
		}
	}
	return frame, nil
}

// DecodedMirrorFrame is one received mirror payload.
type DecodedMirrorFrame struct {
	NodeID   string
	RegionID string
	Class    LaneClass
	Lanes    []LaneMirrorUpdate
}

// DecodeLaneMirrorFrame unpacks a frame produced by EncodeLaneMirrorFrame.
// Truncated tails and unknown versions are refused loudly: a silently
// half-applied mirror is worse than a dropped update.
func DecodeLaneMirrorFrame(frame []byte) (DecodedMirrorFrame, error) {
	var decoded DecodedMirrorFrame
	read := func(n int) ([]byte, error) {
		if len(frame) < n {
			return nil, fmt.Errorf("mirror frame truncated: need %d bytes, hold %d", n, len(frame))
		}
		head := frame[:n]
		frame = frame[n:]
		return head, nil
	}

	version, err := read(1)
	if err != nil {
		return decoded, err
	}
	if version[0] != MirrorWireVersion {
		return decoded, fmt.Errorf("mirror frame version %d unsupported (want %d)", version[0], MirrorWireVersion)
	}
	class, err := read(1)
	if err != nil {
		return decoded, err
	}
	decoded.Class = LaneClass(class[0])

	nodeLen, err := read(1)
	if err != nil {
		return decoded, err
	}
	nodeBytes, err := read(int(nodeLen[0]))
	if err != nil {
		return decoded, err
	}
	decoded.NodeID = string(nodeBytes)

	regionLen, err := read(1)
	if err != nil {
		return decoded, err
	}
	regionBytes, err := read(int(regionLen[0]))
	if err != nil {
		return decoded, err
	}
	decoded.RegionID = string(regionBytes)

	countRaw, err := read(2)
	if err != nil {
		return decoded, err
	}
	laneCount := int(binary.LittleEndian.Uint16(countRaw))
	decoded.Lanes = make([]LaneMirrorUpdate, 0, laneCount)
	for range laneCount {
		record, err := read(mirrorRecordBytes)
		if err != nil {
			return decoded, err
		}
		decoded.Lanes = append(decoded.Lanes, LaneMirrorUpdate{
			Lane:           binary.LittleEndian.Uint16(record[0:]),
			Jurisdiction:   binary.LittleEndian.Uint16(record[2:]),
			MaxConcurrency: binary.LittleEndian.Uint32(record[4:]),
			Inflight:       binary.LittleEndian.Uint32(record[8:]),
			Generation:     binary.LittleEndian.Uint32(record[12:]),
			Class:          LaneClass(record[16]),
			UnitClassMask:  binary.LittleEndian.Uint64(record[17:]),
			AffinityBloom:  binary.LittleEndian.Uint64(record[25:]),
			EwmaNs:         binary.LittleEndian.Uint64(record[33:]),
			TickSeen:       binary.LittleEndian.Uint64(record[41:]),
		})
	}
	return decoded, nil
}
