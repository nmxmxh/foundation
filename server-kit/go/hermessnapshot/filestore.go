package hermessnapshot

// FileStore is a local-filesystem hermes.SnapshotStore. It exists for two
// deployment shapes the objectstore backend does not serve:
//
//  1. same-host durable snapshots (single-node apps, edge nodes, air-gapped
//     installs) where an object store is overhead; and
//  2. the kernel zero-copy artifact lanes from future_practices_research.md
//     lane 7: PromoteLatest moves a multi-megabyte artifact between stores
//     through reflink (FICLONE) or copy_file_range when the kernel and
//     filesystem support them, so artifact bytes never cross userspace. The
//     portable fallback is an ordinary streamed copy — the fast lanes refine
//     the same visible contract (FallbackRefinement), never replace it.
//
// Layout mirrors the objectstore backend: colon-delimited projection names
// become path segments under the root, one immutable artifact file per
// (epoch, watermark), and a small LATEST pointer JSON records the newest
// committed descriptor. Artifact and pointer writes are temp-file + rename so
// readers never observe a torn file.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

// FileStore persists snapshots under a local directory root.
type FileStore struct {
	root string
}

var _ hermes.SnapshotStore = (*FileStore)(nil)

// NewFileStore creates (if needed) and opens a directory-rooted snapshot
// store.
func NewFileStore(root string) (*FileStore, error) {
	cleaned := strings.TrimSpace(root)
	if cleaned == "" {
		return nil, errors.New("hermessnapshot: file store root is required")
	}
	cleaned = filepath.Clean(cleaned)
	if err := os.MkdirAll(cleaned, 0o750); err != nil {
		return nil, fmt.Errorf("hermessnapshot: create file store root: %w", err)
	}
	return &FileStore{root: cleaned}, nil
}

// Save writes the artifact and updates the LATEST pointer, keeping only the
// newest snapshot by (Epoch, Watermark). Duplicate or older saves are skipped
// so retries and racing writers converge, matching the objectstore backend.
func (s *FileStore) Save(ctx context.Context, desc hermes.SnapshotDescriptor, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(desc.Projection) == "" {
		return errors.New("hermessnapshot: descriptor requires a projection")
	}
	if current, _, ok, err := s.Latest(ctx, desc.Projection); err == nil && ok && !newerThan(desc, current) {
		return nil
	}
	if err := os.MkdirAll(s.scopeDir(desc.Projection), 0o750); err != nil {
		return fmt.Errorf("hermessnapshot: create scope dir: %w", err)
	}
	artifact := s.artifactPath(desc)
	if err := writeFileAtomic(artifact, payload); err != nil {
		return err
	}
	pointer, err := json.Marshal(latestPointer{Descriptor: desc, ArtifactKey: filepath.Base(artifact)})
	if err != nil {
		return err
	}
	return writeFileAtomic(s.latestPath(desc.Projection), pointer)
}

// Latest reads the LATEST pointer and the artifact it references, verifying
// the checksum at the storage boundary exactly like the objectstore backend.
// A missing pointer is ok=false, not an error.
func (s *FileStore) Latest(ctx context.Context, projection string) (hermes.SnapshotDescriptor, []byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return hermes.SnapshotDescriptor{}, nil, false, err
	}
	desc, artifact, ok, err := s.latestDescriptor(projection)
	if err != nil || !ok {
		return hermes.SnapshotDescriptor{}, nil, false, err
	}
	payload, err := os.ReadFile(artifact) // #nosec G304 -- path derived from store root + validated pointer basename.
	if err != nil {
		return hermes.SnapshotDescriptor{}, nil, false, fmt.Errorf("hermessnapshot: read artifact: %w", err)
	}
	sum := sha256.Sum256(payload)
	if hex.EncodeToString(sum[:]) != desc.Checksum {
		return hermes.SnapshotDescriptor{}, nil, false, hermes.ErrSnapshotCorrupt
	}
	return desc, payload, true, nil
}

// OpenArtifact opens the newest artifact for streaming without materializing
// it in memory. On Linux, io.Copy from the returned *os.File to a
// *net.TCPConn engages the kernel sendfile/splice fast path via the net
// package's ReadFrom, so served bytes do not cross userspace. The stream is
// NOT checksum-verified here; the descriptor carries the checksum and
// hermes.WarmFromSnapshot re-verifies at the consumer. Callers own Close.
func (s *FileStore) OpenArtifact(ctx context.Context, projection string) (*os.File, hermes.SnapshotDescriptor, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, hermes.SnapshotDescriptor{}, false, err
	}
	desc, artifact, ok, err := s.latestDescriptor(projection)
	if err != nil || !ok {
		return nil, hermes.SnapshotDescriptor{}, false, err
	}
	file, err := os.Open(artifact) // #nosec G304 -- path derived from store root + validated pointer basename.
	if err != nil {
		return nil, hermes.SnapshotDescriptor{}, false, fmt.Errorf("hermessnapshot: open artifact: %w", err)
	}
	return file, desc, true, nil
}

// PromoteLatest clones the newest artifact for a projection into dst without
// routing payload bytes through userspace when the kernel supports it. The
// returned lane names the mechanism actually used: "reflink",
// "copy_file_range", or "userspace" (the portable fallback and the only lane
// on non-Linux builds). Promotion is newest-wins on dst like Save.
func (s *FileStore) PromoteLatest(ctx context.Context, dst *FileStore, projection string) (string, hermes.SnapshotDescriptor, error) {
	if dst == nil {
		return "", hermes.SnapshotDescriptor{}, errors.New("hermessnapshot: promote destination is required")
	}
	if err := ctx.Err(); err != nil {
		return "", hermes.SnapshotDescriptor{}, err
	}
	desc, artifact, ok, err := s.latestDescriptor(projection)
	if err != nil {
		return "", hermes.SnapshotDescriptor{}, err
	}
	if !ok {
		return "", hermes.SnapshotDescriptor{}, errors.New("hermessnapshot: no snapshot to promote")
	}
	if current, _, ok, err := dst.Latest(ctx, projection); err == nil && ok && !strictlyNewerThan(desc, current) {
		return "skipped", current, nil
	}
	if err := os.MkdirAll(dst.scopeDir(projection), 0o750); err != nil {
		return "", hermes.SnapshotDescriptor{}, fmt.Errorf("hermessnapshot: create promote scope dir: %w", err)
	}
	target := dst.artifactPath(desc)
	tmp := target + ".tmp"
	lane, err := cloneFile(tmp, artifact)
	if err != nil {
		removeQuiet(tmp)
		return "", hermes.SnapshotDescriptor{}, fmt.Errorf("hermessnapshot: clone artifact: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		removeQuiet(tmp)
		return "", hermes.SnapshotDescriptor{}, fmt.Errorf("hermessnapshot: publish promoted artifact: %w", err)
	}
	pointer, err := json.Marshal(latestPointer{Descriptor: desc, ArtifactKey: filepath.Base(target)})
	if err != nil {
		return "", hermes.SnapshotDescriptor{}, err
	}
	if err := writeFileAtomic(dst.latestPath(projection), pointer); err != nil {
		return "", hermes.SnapshotDescriptor{}, err
	}
	return lane, desc, nil
}

// latestDescriptor reads and validates the LATEST pointer, returning the
// descriptor and the absolute artifact path.
func (s *FileStore) latestDescriptor(projection string) (hermes.SnapshotDescriptor, string, bool, error) {
	raw, err := os.ReadFile(s.latestPath(projection)) // #nosec G304 -- path derived from store root.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return hermes.SnapshotDescriptor{}, "", false, nil
		}
		return hermes.SnapshotDescriptor{}, "", false, fmt.Errorf("hermessnapshot: read pointer: %w", err)
	}
	var pointer latestPointer
	if err := json.Unmarshal(raw, &pointer); err != nil {
		return hermes.SnapshotDescriptor{}, "", false, fmt.Errorf("hermessnapshot: decode pointer: %w", err)
	}
	base := filepath.Base(pointer.ArtifactKey)
	if base == "." || base == string(filepath.Separator) || strings.Contains(pointer.ArtifactKey, "..") {
		return hermes.SnapshotDescriptor{}, "", false, errors.New("hermessnapshot: pointer artifact key is invalid")
	}
	return pointer.Descriptor, filepath.Join(s.scopeDir(projection), base), true, nil
}

func (s *FileStore) scopeDir(projection string) string {
	segments := strings.Split(strings.TrimSpace(projection), ":")
	parts := append([]string{s.root}, segments...)
	return filepath.Join(parts...)
}

func (s *FileStore) latestPath(projection string) string {
	return filepath.Join(s.scopeDir(projection), latestName)
}

func (s *FileStore) artifactPath(desc hermes.SnapshotDescriptor) string {
	return filepath.Join(s.scopeDir(desc.Projection), fmt.Sprintf("%d-%d%s", desc.Epoch, desc.Watermark, artifactExt))
}

// writeFileAtomic publishes bytes with temp-file + rename so readers never
// observe a torn artifact or pointer.
func writeFileAtomic(path string, payload []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("hermessnapshot: write temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		removeQuiet(tmp)
		return fmt.Errorf("hermessnapshot: publish file: %w", err)
	}
	return nil
}

func removeQuiet(path string) {
	_ = os.Remove(path)
}

// strictlyNewerThan is the promotion comparison: unlike newerThan (which
// treats an equal snapshot as re-saveable for idempotent Save retries),
// promotion skips when the destination already holds the same snapshot, so
// repeated promotions do not re-clone identical artifacts.
func strictlyNewerThan(candidate, existing hermes.SnapshotDescriptor) bool {
	if candidate.Epoch != existing.Epoch {
		return candidate.Epoch > existing.Epoch
	}
	return candidate.Watermark > existing.Watermark
}
