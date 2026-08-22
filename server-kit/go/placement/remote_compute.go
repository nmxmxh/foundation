package placement

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
)

// The remote compute lane turns chunk tickets into executed work over the
// standard frame path. Request frames arrive as
// `compute:chunk:v1:requested`; handlers answer `...:success` or `...:failed`
// with the caller's correlation id intact, per the foundation event lifecycle.
//
// Payload budget: one ticket carries one chunk inline. Chunks are sized well
// under the transport frame ceiling by construction, so no continuation
// protocol exists — oversized requests fail loudly at decode instead of
// silently splitting.

const (
	RemoteComputeRequestedEventType = "compute:chunk:v1:requested"
	RemoteComputeSuccessEventType   = "compute:chunk:v1:success"
	RemoteComputeFailedEventType    = "compute:chunk:v1:failed"

	remoteComputeWireVersion = 1
)

// TicketExecutor performs verified work for one validated ticket.
//
// Implementations own fetching (by projection/scope/index), integrity checks
// against the ticket checksum, and the compute itself. A returned error maps
// to a failed frame; the handler never inspects error text.
type TicketExecutor interface {
	Execute(ctx context.Context, ticket RemoteComputeTicket, payload []byte) ([]byte, error)
}

// TicketExecutorFunc adapts a function to TicketExecutor.
type TicketExecutorFunc func(ctx context.Context, ticket RemoteComputeTicket, payload []byte) ([]byte, error)

// Execute implements TicketExecutor.
func (f TicketExecutorFunc) Execute(ctx context.Context, ticket RemoteComputeTicket, payload []byte) ([]byte, error) {
	return f(ctx, ticket, payload)
}

// EncodeRemoteComputeRequest packs a ticket and its inline chunk payload.
func EncodeRemoteComputeRequest(ticket RemoteComputeTicket, payload []byte) ([]byte, error) {
	if err := ticket.Validate(); err != nil {
		return nil, fmt.Errorf("placement: %w", err)
	}
	checksum, err := hex.DecodeString(ticket.ChecksumHex)
	if err != nil || len(checksum) != 32 {
		return nil, fmt.Errorf("placement: ticket %s/%d checksum must be 32-byte hex", ticket.ProjectionID, ticket.ChunkIndex)
	}
	proj := []byte(ticket.ProjectionID)
	scope := []byte(ticket.ScopeKey)
	if len(proj) == 0 || len(proj) > 255 || len(scope) > 255 {
		return nil, fmt.Errorf("placement: projection id 1..255 bytes, scope 0..255")
	}

	frame := make([]byte, 0, 8+len(proj)+len(scope)+len(payload))
	frame = append(frame, remoteComputeWireVersion, byte(len(proj)))
	frame = append(frame, proj...)
	frame = append(frame, byte(len(scope)))
	frame = append(frame, scope...)
	frame = binary.LittleEndian.AppendUint32(frame, ticket.ChunkIndex)
	frame = binary.LittleEndian.AppendUint32(frame, ticket.PayloadLen)
	frame = append(frame, checksum...)
	frame = binary.LittleEndian.AppendUint64(frame, ticket.RequiredClassMask)
	frame = binary.LittleEndian.AppendUint16(frame, ticket.Jurisdiction)
	frame = binary.LittleEndian.AppendUint64(frame, ticket.DeadlineNs)
	frame = binary.LittleEndian.AppendUint64(frame, ticket.AffinityKey)
	frame = binary.LittleEndian.AppendUint32(frame, uint32(len(payload)))
	frame = append(frame, payload...)
	return frame, nil
}

// DecodeRemoteComputeRequest unpacks what EncodeRemoteComputeRequest packed.
func DecodeRemoteComputeRequest(frame []byte) (RemoteComputeTicket, []byte, error) {
	var ticket RemoteComputeTicket
	read := func(n int) ([]byte, error) {
		if len(frame) < n {
			return nil, fmt.Errorf("remote compute frame truncated: need %d hold %d", n, len(frame))
		}
		head := frame[:n]
		frame = frame[n:]
		return head, nil
	}
	version, err := read(1)
	if err != nil {
		return ticket, nil, err
	}
	if version[0] != remoteComputeWireVersion {
		return ticket, nil, fmt.Errorf("remote compute wire version %d unsupported", version[0])
	}
	projLen, err := read(1)
	if err != nil {
		return ticket, nil, err
	}
	projBytes, err := read(int(projLen[0]))
	if err != nil {
		return ticket, nil, err
	}
	ticket.ProjectionID = string(projBytes)
	scopeLen, err := read(1)
	if err != nil {
		return ticket, nil, err
	}
	scopeBytes, err := read(int(scopeLen[0]))
	if err != nil {
		return ticket, nil, err
	}
	ticket.ScopeKey = string(scopeBytes)

	indexRaw, err := read(4)
	if err != nil {
		return ticket, nil, err
	}
	ticket.ChunkIndex = binary.LittleEndian.Uint32(indexRaw)
	lenRaw, err := read(4)
	if err != nil {
		return ticket, nil, err
	}
	ticket.PayloadLen = binary.LittleEndian.Uint32(lenRaw)
	checksum, err := read(32)
	if err != nil {
		return ticket, nil, err
	}
	ticket.ChecksumHex = hex.EncodeToString(checksum)
	maskRaw, err := read(8)
	if err != nil {
		return ticket, nil, err
	}
	ticket.RequiredClassMask = binary.LittleEndian.Uint64(maskRaw)
	jurRaw, err := read(2)
	if err != nil {
		return ticket, nil, err
	}
	ticket.Jurisdiction = binary.LittleEndian.Uint16(jurRaw)
	deadlineRaw, err := read(8)
	if err != nil {
		return ticket, nil, err
	}
	ticket.DeadlineNs = binary.LittleEndian.Uint64(deadlineRaw)
	affinityRaw, err := read(8)
	if err != nil {
		return ticket, nil, err
	}
	ticket.AffinityKey = binary.LittleEndian.Uint64(affinityRaw)
	payloadLenRaw, err := read(4)
	if err != nil {
		return ticket, nil, err
	}
	payloadLen := binary.LittleEndian.Uint32(payloadLenRaw)
	payload, err := read(int(payloadLen))
	if err != nil {
		return ticket, nil, err
	}
	// Note: ticket.PayloadLen is the chunk's declared size inside its
	// artifact; the inline payload may be a window of it or empty when the
	// executor fetches by handle. Integrity is the checksum's job.
	return ticket, payload, nil
}

// RemoteComputeHandler adapts a TicketExecutor into a frame handler following
// the requested→success/failed lifecycle. Validation failures and execution
// errors both map to failed frames carrying the reason; correlation ids always
// survive the round trip.
func RemoteComputeHandler(executor TicketExecutor) grpcsvc.FrameHandler {
	return func(_ context.Context, frame grpcsvc.Frame) (grpcsvc.Frame, error) {
		ticket, payload, err := DecodeRemoteComputeRequest(frame.Payload)
		if err != nil {
			return failedRemoteFrame(frame.CorrelationID, err), nil
		}
		if err := ticket.Validate(); err != nil {
			return failedRemoteFrame(frame.CorrelationID, err), nil
		}
		result, execErr := executor.Execute(context.Background(), ticket, payload)
		if execErr != nil {
			return failedRemoteFrame(frame.CorrelationID, execErr), nil
		}
		return grpcsvc.Frame{
			EventType:     RemoteComputeSuccessEventType,
			Payload:       result,
			CorrelationID: frame.CorrelationID,
			SchemaVersion: frame.SchemaVersion,
		}, nil
	}
}

func failedRemoteFrame(correlationID string, cause error) grpcsvc.Frame {
	return grpcsvc.Frame{
		EventType:     RemoteComputeFailedEventType,
		Payload:       []byte(cause.Error()),
		CorrelationID: correlationID,
	}
}
