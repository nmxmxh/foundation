package hermes

import (
	"context"
	"strconv"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// This file is the read-side inverse of contract.go. contract.go maps inbound
// foundation.v1.RecordMutation events into hermes Events (the write/apply
// direction). The projection read path (snapshots and live deltas served by the
// projection gateway) needs the opposite mapping: materialized hermes records
// back into RecordMutation protos so the snapshot and the delta stream share one
// canonical wire shape (RecordMutationBatch carried in an events.Envelope).

// FormatWatermark renders a Hermes source watermark as the resume token used by
// the projection transport. It is the inverse of ParseWatermark.
func FormatWatermark(watermark uint64) string {
	if watermark == 0 {
		return ""
	}
	return strconv.FormatUint(watermark, 10)
}

// ParseWatermark parses a resume token produced by FormatWatermark. An empty or
// malformed token resolves to zero (full snapshot / resume from the beginning).
func ParseWatermark(token string) uint64 {
	value, err := strconv.ParseUint(token, 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// MutationFromView projects a borrowed RecordView into an owned RecordMutation
// proto with the supplied operation. The returned proto owns all of its data, so
// it remains valid after the ForEachView callback that produced the view returns.
func MutationFromView(view RecordView, operation Operation) *foundationpb.RecordMutation {
	return mutationFromParts(operation, view.Version, view.Domain, view.Collection,
		view.OrganizationID, view.RecordID, view.Data, view.Vector, view.CreatedAt, view.UpdatedAt)
}

// MutationFromRecord projects a materialized DomainRecord into an owned
// RecordMutation proto with the supplied operation and final applied version. It
// is the accepted-delta builder: the projection gateway calls it for each event
// hermes actually accepted, stamping the version hermes assigned, so the live
// stream reflects the source of truth rather than the client's submission.
func MutationFromRecord(rec database.DomainRecord, operation Operation, version uint64) *foundationpb.RecordMutation {
	return mutationFromParts(operation, version, rec.Domain, rec.Collection,
		rec.OrganizationID, rec.RecordID, rec.Data, rec.Vector, rec.CreatedAt, rec.UpdatedAt)
}

func mutationFromParts(operation Operation, version uint64, domain, collection, organizationID, recordID string, data database.RecordData, vector []float32, createdAt, updatedAt time.Time) *foundationpb.RecordMutation {
	mutation := &foundationpb.RecordMutation{
		Operation:      operationToProto(operation),
		Version:        version,
		Domain:         domain,
		Collection:     collection,
		OrganizationId: organizationID,
		RecordId:       recordID,
		Fields:         fieldsToProto(data),
	}
	if len(vector) > 0 {
		mutation.Vector = append([]float32(nil), vector...)
	}
	if !createdAt.IsZero() {
		mutation.CreatedAt = timestamppb.New(createdAt)
	}
	if !updatedAt.IsZero() {
		mutation.UpdatedAt = timestamppb.New(updatedAt)
	}
	return mutation
}

func fieldsToProto(data database.RecordData) []*foundationpb.FieldValue {
	if len(data) == 0 {
		return nil
	}
	fields := make([]*foundationpb.FieldValue, 0, len(data))
	for _, field := range data {
		scalar, ok := scalarToProto(field.Value)
		if !ok {
			continue
		}
		fields = append(fields, &foundationpb.FieldValue{Name: field.Name, Value: scalar})
	}
	return fields
}

func scalarToProto(value database.RecordValue) (*foundationpb.ScalarValue, bool) {
	switch value.Kind {
	case database.RecordValueString:
		return &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_StringValue{StringValue: value.Text}}, true
	case database.RecordValueBool:
		return &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_BoolValue{BoolValue: value.Text == "true"}}, true
	case database.RecordValueInt:
		parsed, err := strconv.ParseInt(value.Text, 10, 64)
		if err != nil {
			return nil, false
		}
		return &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_Int64Value{Int64Value: parsed}}, true
	case database.RecordValueUint:
		parsed, err := strconv.ParseUint(value.Text, 10, 64)
		if err != nil {
			return nil, false
		}
		return &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_Uint64Value{Uint64Value: parsed}}, true
	case database.RecordValueFloat:
		parsed, err := strconv.ParseFloat(value.Text, 64)
		if err != nil {
			return nil, false
		}
		return &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_DoubleValue{DoubleValue: parsed}}, true
	case database.RecordValueRaw:
		return &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_BytesValue{BytesValue: append([]byte(nil), value.Raw...)}}, true
	default:
		// RecordValueNull and unknown kinds carry no scalar payload.
		return nil, false
	}
}

func operationToProto(operation Operation) foundationpb.ProjectionOperation {
	switch operation {
	case OperationPatch:
		return foundationpb.ProjectionOperation_PROJECTION_OPERATION_PATCH
	case OperationDelete:
		return foundationpb.ProjectionOperation_PROJECTION_OPERATION_DELETE
	default:
		return foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT
	}
}

// Snapshot is the materialized read model for one projection scope at a
// consistent epoch, expressed as upsert mutations (newest-first by version) plus
// resume/pagination cursors.
type Snapshot struct {
	Mutations []*foundationpb.RecordMutation
	Epoch     uint64
	Watermark uint64
	// NextCursor is the version of the oldest record in this page; present it as
	// beforeVersion to fetch the next (older) page. Zero when no more pages.
	NextCursor uint64
	// HasMore is true when older records remain beyond this page.
	HasMore bool
}

// SnapshotProjection reads the materialized records for a projection as upsert
// mutations, newest-first, bounded by query.Limit and fence.
//
// When sinceVersion > 0 the read is incremental: only records whose version
// advanced past sinceVersion are emitted (forward catch-up). Because projections
// are already versioned, a client that holds prior state resumes by sending its
// watermark and receives just the changed records — not the whole collection.
// (Records deleted while the client was disconnected are reconciled via the live
// delta stream, not the live-set read.)
func (s *Store) SnapshotProjection(ctx context.Context, projection string, query Query, fence Fence, sinceVersion uint64) (Snapshot, error) {
	return s.SnapshotPage(ctx, projection, query, fence, sinceVersion, 0)
}

// SnapshotPage is the keyset-paginated read. It returns up to query.Limit
// records, newest-first by version, in the half-open version window
// (sinceVersion, beforeVersion). beforeVersion == 0 means "from the newest"
// (first page); set it to a prior page's NextCursor to fetch the next (older)
// page. This backfills scopes larger than the limit in O(limit) per request —
// no unbounded scan — by walking hermes's version-descending ordered index with
// early termination.
func (s *Store) SnapshotPage(ctx context.Context, projection string, query Query, fence Fence, sinceVersion, beforeVersion uint64) (Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return Snapshot{}, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return Snapshot{}, err
	}
	return part.pageMutations(ctx, query, fence, sinceVersion, beforeVersion)
}

func (p *partition) pageMutations(ctx context.Context, query Query, fence Fence, sinceVersion, beforeVersion uint64) (Snapshot, error) {
	if err := p.waitForStable(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := p.checkFence(fence); err != nil {
		return Snapshot{}, err
	}
	query = normalizeQuery(query)
	if query.OrganizationID == "" {
		return Snapshot{}, ErrInvalidEvent
	}
	registry := p.activeRegistry()
	ordered := p.orderedCandidateIndex(registry, query)
	snapshot := Snapshot{Epoch: p.epoch.Load(), Watermark: p.watermark.Load()}
	if ordered == nil {
		return snapshot, nil
	}
	limit := query.Limit
	capacity := limit
	if capacity <= 0 {
		capacity = ordered.len()
	}
	mutations := make([]*foundationpb.RecordMutation, 0, capacity)
	var loopErr error
	// The ordered index yields entries version-descending. Skip entries newer
	// than the cursor, stop once we cross the sinceVersion floor, and emit each
	// live record (recordForOrderEntry skips superseded versions) until the limit.
	ordered.forEachOrderDesc(func(order recordOrderEntry) bool {
		if beforeVersion > 0 && order.version >= beforeVersion {
			return true
		}
		if order.version <= sinceVersion {
			return false
		}
		entry, ok := p.recordForOrderEntry(registry, order)
		if !ok || !recordMatches(entry.record, p.spec, query) {
			return true
		}
		if err := ctxErr(ctx); err != nil {
			loopErr = err
			return false
		}
		mutations = append(mutations, MutationFromRecord(entry.record, OperationUpsert, entry.version))
		snapshot.NextCursor = entry.version
		if limit > 0 && len(mutations) >= limit {
			snapshot.HasMore = true
			return false
		}
		return true
	})
	if loopErr != nil {
		return Snapshot{}, loopErr
	}
	if !snapshot.HasMore {
		snapshot.NextCursor = 0
	}
	snapshot.Mutations = mutations
	return snapshot, nil
}
