// Package hermessnapshot provides durable hermes.SnapshotStore backends. It
// lives outside the core hermes package so that hermes consumers do not
// inherit backend dependencies unless they opt into durable, shared snapshots.
//
// Two backends ship: Store (object storage, this file) for shared cross-node
// snapshots, and FileStore (filestore.go) for same-host durable snapshots with
// kernel zero-copy promotion lanes (reflink/copy_file_range) and
// sendfile-path artifact serving.
//
// Layout: artifacts are written to tenant-scoped keys derived from the
// colon-delimited, org-embedding projection name, and a small LATEST pointer
// object records the newest committed descriptor so Latest is a two-object read
// instead of a bucket listing (which the object store does not expose).
package hermessnapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
)

const (
	defaultPrefix       = "hermes/snapshots"
	latestName          = "LATEST"
	artifactExt         = ".ovsnap"
	artifactContentType = "application/vnd.ovasabi.hermes-snapshot"
)

// Store is an objectstore-backed hermes.SnapshotStore.
type Store struct {
	obj    *objectstore.Store
	prefix string
}

var _ hermes.SnapshotStore = (*Store)(nil)

// latestPointer is the small JSON object that records the newest committed
// snapshot for a projection.
type latestPointer struct {
	Descriptor  hermes.SnapshotDescriptor `json:"descriptor"`
	ArtifactKey string                    `json:"artifact_key"`
}

// New returns an objectstore-backed snapshot store. prefix defaults to
// "hermes/snapshots" when empty.
func New(obj *objectstore.Store, prefix string) (*Store, error) {
	if obj == nil {
		return nil, errors.New("hermessnapshot requires an object store")
	}
	p := strings.TrimSpace(prefix)
	if p == "" {
		p = defaultPrefix
	}
	return &Store{obj: obj, prefix: strings.TrimRight(p, "/")}, nil
}

// Save writes the artifact and updates the LATEST pointer. It is idempotent and
// race-safe: if a newer-or-equal snapshot is already committed the write is
// skipped, so independent writers converge on newest-wins.
func (s *Store) Save(ctx context.Context, desc hermes.SnapshotDescriptor, payload []byte) error {
	if strings.TrimSpace(desc.Projection) == "" {
		return errors.New("hermessnapshot: descriptor requires a projection")
	}
	if current, _, ok, err := s.Latest(ctx, desc.Projection); err == nil && ok && !newerThan(desc, current) {
		return nil
	}
	artifactKey := s.artifactKey(desc)
	if _, err := s.obj.PutBytes(ctx, artifactKey, payload, objectstore.PutOptions{
		ContentType: artifactContentType,
		Metadata: map[string]string{
			"projection": desc.Projection,
			"epoch":      strconv.FormatUint(desc.Epoch, 10),
			"watermark":  strconv.FormatUint(desc.Watermark, 10),
			"checksum":   desc.Checksum,
		},
	}); err != nil {
		return err
	}
	pointer, err := json.Marshal(latestPointer{Descriptor: desc, ArtifactKey: artifactKey})
	if err != nil {
		return err
	}
	if _, err := s.obj.PutBytes(ctx, s.latestKey(desc.Projection), pointer, objectstore.PutOptions{
		ContentType: "application/json",
	}); err != nil {
		return err
	}
	return nil
}

// Latest reads the LATEST pointer and the artifact it references. A missing
// pointer is reported as ok=false (no snapshot yet), not an error, matching the
// in-memory reference store. The artifact checksum is verified defensively at
// the storage boundary; hermes.WarmFromSnapshot verifies it again.
func (s *Store) Latest(ctx context.Context, projection string) (hermes.SnapshotDescriptor, []byte, bool, error) {
	raw, err := s.obj.ReadBytes(ctx, s.latestKey(projection))
	if err != nil {
		return hermes.SnapshotDescriptor{}, nil, false, nil
	}
	var pointer latestPointer
	if err := json.Unmarshal(raw, &pointer); err != nil {
		return hermes.SnapshotDescriptor{}, nil, false, err
	}
	payload, err := s.obj.ReadBytes(ctx, pointer.ArtifactKey)
	if err != nil {
		return hermes.SnapshotDescriptor{}, nil, false, err
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != pointer.Descriptor.Checksum {
		return hermes.SnapshotDescriptor{}, nil, false, hermes.ErrSnapshotCorrupt
	}
	return pointer.Descriptor, payload, true, nil
}

func (s *Store) scopeKey(projection string) string {
	// Colons in projection names (prefix:domain:collection:org) become path
	// segments, keeping each tenant's artifacts under a distinct prefix.
	safe := strings.ReplaceAll(strings.TrimSpace(projection), ":", "/")
	return s.prefix + "/" + safe
}

func (s *Store) latestKey(projection string) string {
	return s.scopeKey(projection) + "/" + latestName
}

func (s *Store) artifactKey(desc hermes.SnapshotDescriptor) string {
	return fmt.Sprintf("%s/%d-%d%s", s.scopeKey(desc.Projection), desc.Epoch, desc.Watermark, artifactExt)
}

func newerThan(candidate, existing hermes.SnapshotDescriptor) bool {
	if candidate.Epoch != existing.Epoch {
		return candidate.Epoch > existing.Epoch
	}
	return candidate.Watermark >= existing.Watermark
}
