package placement

import (
	"encoding/hex"
	"fmt"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

// RemoteComputeTicket is one chunk-sized unit of remote work.
//
// Hermes already chunks snapshots into independently checksummed pieces
// (ChunkDescriptor). A ticket binds one such chunk to a placement class and a
// deadline — everything a remote executor needs to fetch, verify, compute,
// and return a result frame. This is the mesh equivalent of routing one 1MB
// chunk to its lowest-latency holder: the data location decides the work
// location, and the checksum decides whether the answer can be trusted.
type RemoteComputeTicket struct {
	ProjectionID string
	ScopeKey     string
	ChunkIndex   uint32
	PayloadLen   uint32
	ChecksumHex  string

	RequiredClassMask uint64
	Jurisdiction      uint16
	DeadlineNs        uint64
	AffinityKey       uint64
}

// TicketFromChunkDescriptor builds a ticket from one manifest entry. The
// checksum rides as hex so tickets stay printable in logs and frames.
func TicketFromChunkDescriptor(
	projectionID, scopeKey string,
	chunk hermes.ChunkDescriptor,
	classMask uint64,
	jurisdiction uint16,
	deadlineNs uint64,
) RemoteComputeTicket {
	return RemoteComputeTicket{
		ProjectionID:      projectionID,
		ScopeKey:          scopeKey,
		ChunkIndex:        chunk.Index,
		PayloadLen:        chunk.PayloadLength,
		ChecksumHex:       hex.EncodeToString(chunk.Checksum[:]),
		RequiredClassMask: classMask,
		Jurisdiction:      jurisdiction,
		DeadlineNs:        deadlineNs,
	}
}

// Validate refuses tickets that could not be executed or verified.
func (t RemoteComputeTicket) Validate() error {
	if t.ProjectionID == "" {
		return fmt.Errorf("remote ticket requires a projection id")
	}
	if t.PayloadLen == 0 {
		return fmt.Errorf("remote ticket %s/%d requires payload bytes", t.ProjectionID, t.ChunkIndex)
	}
	if len(t.ChecksumHex) != 64 {
		return fmt.Errorf("remote ticket %s/%d requires the full sha256 checksum", t.ProjectionID, t.ChunkIndex)
	}
	if t.RequiredClassMask == 0 {
		return fmt.Errorf("remote ticket %s/%d requires a capability class", t.ProjectionID, t.ChunkIndex)
	}
	if t.DeadlineNs == 0 {
		return fmt.Errorf("remote ticket %s/%d requires a deadline", t.ProjectionID, t.ChunkIndex)
	}
	return nil
}
