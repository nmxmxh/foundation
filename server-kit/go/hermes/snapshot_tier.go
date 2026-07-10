package hermes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"google.golang.org/protobuf/proto"
)

// The durable shared snapshot tier lets a cold partition warm from a durable,
// versioned artifact plus a bounded tail replay instead of a full source scan.
//
// A snapshot is NOT a source of truth: it is a materialized cache of a prefix of
// the event stream, always reconstructable from the source. Warming from a
// snapshot restores the partition to the artifact's watermark; the caller then
// merges the tail (events with version > descriptor.Watermark) via the normal
// ApplyRecords/ApplyEnvelopes paths. If a snapshot is missing, corrupt, or
// scope-mismatched, callers fall back to the existing Rebuild path — the warm
// result is a refinement of today's behavior, never worse and never wrong.

var (
	// ErrSnapshotCorrupt is returned when a loaded artifact fails checksum
	// verification. The partition is left untouched; callers fall back to
	// Rebuild.
	ErrSnapshotCorrupt = errors.New("hermes snapshot checksum mismatch")
	// ErrSnapshotScope is returned when a loaded artifact's declared
	// domain/collection scope disagrees with the target projection. It is the
	// seal that a snapshot for one scope can never warm another.
	ErrSnapshotScope = errors.New("hermes snapshot scope mismatch")
)

// SnapshotDescriptor is the durable metadata for one materialized projection
// snapshot. Watermark is the source cursor (LSN-equivalent) the snapshot was
// pinned at: the caller merges events with version > Watermark to reach live.
type SnapshotDescriptor struct {
	Projection     string
	Domain         string
	Collection     string
	OrganizationID string
	Epoch          uint64
	Watermark      uint64
	Records        int64
	Bytes          int64
	Checksum       string // sha256 hex of the payload
}

// SnapshotStore persists and loads durable, versioned projection snapshots.
// Implementations MUST be tenant-scoped in their key derivation and treat the
// payload as opaque bytes. Newest-wins is defined by (Epoch, Watermark).
type SnapshotStore interface {
	// Save writes the artifact for a projection scope. Idempotent on
	// (Projection, Epoch, Watermark): a duplicate save must not corrupt or
	// regress the newest committed snapshot.
	Save(ctx context.Context, desc SnapshotDescriptor, payload []byte) error
	// Latest returns the newest committed snapshot for a projection, or
	// ok=false when none exists yet.
	Latest(ctx context.Context, projection string) (SnapshotDescriptor, []byte, bool, error)
}

// ExportSnapshot serializes a projection's current state into a canonical
// RecordMutationBatch payload plus a descriptor, pinned at the partition's
// current epoch and source watermark. Records are read through the borrowed
// zero-copy view path, so the export does not touch the source database.
func (s *Store) ExportSnapshot(ctx context.Context, projection string, query Query) (SnapshotDescriptor, []byte, error) {
	if err := ctxErr(ctx); err != nil {
		return SnapshotDescriptor{}, nil, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return SnapshotDescriptor{}, nil, err
	}
	epoch := part.epoch.Load()
	watermark := part.watermark.Load()
	var mutations []*foundationpb.RecordMutation
	if _, err := part.forEachView(ctx, query, Fence{}, func(view RecordView) error {
		mutations = append(mutations, MutationFromView(view, OperationUpsert))
		return nil
	}); err != nil {
		return SnapshotDescriptor{}, nil, err
	}
	payload, err := proto.Marshal(&foundationpb.RecordMutationBatch{Mutations: mutations})
	if err != nil {
		return SnapshotDescriptor{}, nil, err
	}
	sum := sha256.Sum256(payload)
	desc := SnapshotDescriptor{
		Projection:     projection,
		Domain:         part.spec.Domain,
		Collection:     part.spec.Collection,
		OrganizationID: strings.TrimSpace(query.OrganizationID),
		Epoch:          epoch,
		Watermark:      watermark,
		Records:        int64(len(mutations)),
		Bytes:          int64(len(payload)),
		Checksum:       hex.EncodeToString(sum[:]),
	}
	return desc, payload, nil
}

// ExportSnapshotColumnar serializes a projection into the HCS1 columnar
// artifact: same descriptor semantics as ExportSnapshot, set-based payload.
// This is the write half of the columnar artifact lane — decode cost scales
// with columns instead of records×fields, and repeated identity strings and
// field names are stored once.
func (s *Store) ExportSnapshotColumnar(ctx context.Context, projection string, query Query) (SnapshotDescriptor, []byte, error) {
	if err := ctxErr(ctx); err != nil {
		return SnapshotDescriptor{}, nil, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return SnapshotDescriptor{}, nil, err
	}
	epoch := part.epoch.Load()
	watermark := part.watermark.Load()
	var records []database.DomainRecord
	if _, err := part.forEachView(ctx, query, Fence{}, func(view RecordView) error {
		records = append(records, recordFromView(view))
		return nil
	}); err != nil {
		return SnapshotDescriptor{}, nil, err
	}
	payload, err := encodeColumnarSnapshot(records)
	if err != nil {
		return SnapshotDescriptor{}, nil, err
	}
	sum := sha256.Sum256(payload)
	return SnapshotDescriptor{
		Projection:     projection,
		Domain:         part.spec.Domain,
		Collection:     part.spec.Collection,
		OrganizationID: strings.TrimSpace(query.OrganizationID),
		Epoch:          epoch,
		Watermark:      watermark,
		Records:        int64(len(records)),
		Bytes:          int64(len(payload)),
		Checksum:       hex.EncodeToString(sum[:]),
	}, payload, nil
}

// SaveSnapshot exports the projection and writes the artifact to store. It is
// the off-hot-path materialization step; run it from a bounded background
// worker. Artifacts are written in the columnar HCS1 format; readers sniff the
// magic, so row-proto artifacts already in stores keep loading unchanged.
func (s *Store) SaveSnapshot(ctx context.Context, projection string, query Query, store SnapshotStore) (SnapshotDescriptor, error) {
	if store == nil {
		return SnapshotDescriptor{}, errors.New("hermes snapshot store is required")
	}
	desc, payload, err := s.ExportSnapshotColumnar(ctx, projection, query)
	if err != nil {
		return SnapshotDescriptor{}, err
	}
	if err := store.Save(ctx, desc, payload); err != nil {
		return SnapshotDescriptor{}, err
	}
	return desc, nil
}

// SnapshotShadowReport is the evidence one shadow comparison produces: how the
// newest durable artifact diverges from the live (source-rebuilt) partition.
// It is the dual-load-and-diff phase that must accumulate clean matches before
// any warm path prefers the snapshot over a source Rebuild.
type SnapshotShadowReport struct {
	Descriptor      SnapshotDescriptor
	LiveRecords     int
	SnapshotRecords int
	// MissingInSnapshot counts live records the artifact lacks (artifact is
	// stale-behind); ExtraInSnapshot counts artifact records the live partition
	// lacks (deleted since the artifact); DataMismatches counts records present
	// in both with differing data.
	MissingInSnapshot int
	ExtraInSnapshot   int
	DataMismatches    int
}

// Match reports whether the artifact reproduces the live partition exactly.
func (r SnapshotShadowReport) Match() bool {
	return r.MissingInSnapshot == 0 && r.ExtraInSnapshot == 0 && r.DataMismatches == 0
}

// ShadowCompareSnapshot diffs the newest durable snapshot against the live
// partition WITHOUT mutating either — the read-only shadow half of the
// snapshot-warm rollout. ok=false with nil error means no artifact exists yet.
// A corrupt or scope-mismatched artifact returns an error; the caller records
// it as shadow evidence, never as a serving failure (FallbackRefinement: the
// shadow lane cannot affect the served result).
func (s *Store) ShadowCompareSnapshot(ctx context.Context, projection string, query Query, store SnapshotStore) (SnapshotShadowReport, bool, error) {
	report := SnapshotShadowReport{}
	if store == nil {
		return report, false, errors.New("hermes snapshot store is required")
	}
	if err := ctxErr(ctx); err != nil {
		return report, false, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return report, false, err
	}
	desc, payload, ok, err := store.Latest(ctx, projection)
	if err != nil || !ok {
		return report, false, err
	}
	report.Descriptor = desc
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != desc.Checksum {
		return report, false, ErrSnapshotCorrupt
	}
	if desc.Domain != part.spec.Domain || desc.Collection != part.spec.Collection {
		return report, false, ErrSnapshotScope
	}
	// Canonical-JSON record bodies keyed by identity: deterministic equality
	// without per-field walking. The shadow lane runs on cold warms only, so
	// the marshal cost is acceptable evidence-gathering overhead. Format is
	// sniffed by magic, so legacy row-proto artifacts compare unchanged.
	artifact := make(map[string]string)
	if err := streamSnapshotRecords(payload, func(rec database.DomainRecord) error {
		body, err := rec.Data.MarshalJSON()
		if err != nil {
			return err
		}
		artifact[recordKey(rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID)] = string(body)
		return nil
	}); err != nil {
		return report, false, err
	}
	report.SnapshotRecords = len(artifact)

	_, err = s.ForEachView(ctx, projection, query, Fence{}, func(view RecordView) error {
		report.LiveRecords++
		rec := recordFromView(view)
		key := recordKey(rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID)
		body, ok := artifact[key]
		if !ok {
			report.MissingInSnapshot++
			return nil
		}
		delete(artifact, key)
		live, err := rec.Data.MarshalJSON()
		if err != nil {
			return err
		}
		if string(live) != body {
			report.DataMismatches++
		}
		return nil
	})
	if err != nil {
		return report, false, err
	}
	report.ExtraInSnapshot = len(artifact)
	return report, true, nil
}

// WarmFromSnapshot loads the newest durable snapshot for a projection and
// atomically replaces the partition with it, restoring the partition's source
// watermark to the artifact's cursor. It returns the descriptor so the caller
// can merge the tail (events with version > desc.Watermark) to reach live.
//
// ok=false with a nil error means no snapshot exists yet: the caller should use
// the full Rebuild path. A corrupt or scope-mismatched artifact returns an error
// and leaves the partition untouched.
func (s *Store) WarmFromSnapshot(ctx context.Context, projection string, store SnapshotStore) (SnapshotDescriptor, bool, error) {
	if store == nil {
		return SnapshotDescriptor{}, false, errors.New("hermes snapshot store is required")
	}
	if err := ctxErr(ctx); err != nil {
		return SnapshotDescriptor{}, false, err
	}
	part, err := s.partition(projection)
	if err != nil {
		return SnapshotDescriptor{}, false, err
	}
	desc, payload, ok, err := store.Latest(ctx, projection)
	if err != nil || !ok {
		return SnapshotDescriptor{}, false, err
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != desc.Checksum {
		return desc, false, ErrSnapshotCorrupt
	}
	if desc.Domain != part.spec.Domain || desc.Collection != part.spec.Collection {
		return desc, false, ErrSnapshotScope
	}
	// Stream records into the atomic bulk load rather than materializing a
	// full []DomainRecord first, matching streamRebuildRecords in rebuild.go.
	// Format is sniffed by magic: columnar (HCS1) artifacts decode per column;
	// legacy row-proto artifacts keep loading unchanged.
	if _, err := part.bulkLoadFrom(ctx, func(visit database.RecordVisitor) error {
		return streamSnapshotRecords(payload, visit)
	}); err != nil {
		return desc, false, err
	}
	// bulkLoad sets the watermark to the load counter; restore the source
	// cursor so the tail merge resumes from the correct position.
	part.watermark.Store(desc.Watermark)
	return desc, true, nil
}

// MemorySnapshotStore is an in-memory SnapshotStore: the reference
// implementation, the local test/bench backend, and the shape an
// objectstore-backed store must satisfy. Newest-wins by (Epoch, Watermark).
type MemorySnapshotStore struct {
	mu    sync.RWMutex
	items map[string]memorySnapshot
}

type memorySnapshot struct {
	desc    SnapshotDescriptor
	payload []byte
}

// NewMemorySnapshotStore returns an empty in-memory snapshot store.
func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{items: map[string]memorySnapshot{}}
}

// Save stores the artifact, keeping only the newest by (Epoch, Watermark). A
// duplicate or older save is ignored, so retries and racing writers are safe.
func (m *MemorySnapshotStore) Save(_ context.Context, desc SnapshotDescriptor, payload []byte) error {
	if strings.TrimSpace(desc.Projection) == "" {
		return errors.New("hermes snapshot descriptor requires a projection")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.items[desc.Projection]; ok && !newerThan(desc, existing.desc) {
		return nil
	}
	stored := make([]byte, len(payload))
	copy(stored, payload)
	m.items[desc.Projection] = memorySnapshot{desc: desc, payload: stored}
	return nil
}

// Latest returns a defensive copy of the newest artifact for a projection.
func (m *MemorySnapshotStore) Latest(_ context.Context, projection string) (SnapshotDescriptor, []byte, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, ok := m.items[projection]
	if !ok {
		return SnapshotDescriptor{}, nil, false, nil
	}
	out := make([]byte, len(item.payload))
	copy(out, item.payload)
	return item.desc, out, true, nil
}

func newerThan(candidate, existing SnapshotDescriptor) bool {
	if candidate.Epoch != existing.Epoch {
		return candidate.Epoch > existing.Epoch
	}
	return candidate.Watermark >= existing.Watermark
}
