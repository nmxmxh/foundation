package hermes

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

// Tiered chunked snapshot artifact ("HCS2"):
// When buffers scale past 500k+ records, serializing a single monolithic
// compressed stream causes memory allocation spikes. The HCS2 container
// partitions the snapshot into independently checksummed columnar chunks
// described by a root manifest header.
//
// Layout (all integers little-endian u32 unless noted):
//   magic "HCS2"
//   totalRecords (u32)
//   chunkCount   (u32)
//   per chunk in header:
//     chunkIndex    (u32)
//     recordCount   (u32)
//     payloadOffset (u32)
//     payloadLength (u32)
//     checksum      ([32]byte sha256)
//   chunk payloads (each an independent HCS1 columnar chunk)

var chunkedSnapshotMagic = [4]byte{'H', 'C', 'S', '2'}

const DefaultSnapshotChunkSize = 10000

// ChunkDescriptor describes one segment of a chunked snapshot.
type ChunkDescriptor struct {
	Index         uint32   `json:"index"`
	RecordCount   uint32   `json:"record_count"`
	PayloadOffset uint32   `json:"payload_offset"`
	PayloadLength uint32   `json:"payload_length"`
	Checksum      [32]byte `json:"checksum"`
}

// SnapshotChunkManifest represents the decoded header manifest of an HCS2 artifact.
type SnapshotChunkManifest struct {
	TotalRecords uint32            `json:"total_records"`
	Chunks       []ChunkDescriptor `json:"chunks"`
}

func isChunkedSnapshot(payload []byte) bool {
	return len(payload) >= 4 && [4]byte(payload[:4]) == chunkedSnapshotMagic
}

// SnapshotChunkWriter encodes materialized records in partitioned HCS2 chunks.
type SnapshotChunkWriter struct {
	chunkSize int
}

// NewSnapshotChunkWriter creates a chunk writer with the given chunk record bound.
func NewSnapshotChunkWriter(chunkSize int) *SnapshotChunkWriter {
	if chunkSize <= 0 {
		chunkSize = DefaultSnapshotChunkSize
	}
	return &SnapshotChunkWriter{chunkSize: chunkSize}
}

// Encode serializes records into the HCS2 chunked format.
func (w *SnapshotChunkWriter) Encode(records []database.DomainRecord) ([]byte, error) {
	n := len(records)
	if n > math.MaxUint32 {
		return nil, errors.New("hermes chunked snapshot exceeds u32 record count")
	}

	if n == 0 {
		// Empty snapshot encoded with 0 chunks.
		buf := make([]byte, 12)
		copy(buf[:4], chunkedSnapshotMagic[:])
		binary.LittleEndian.PutUint32(buf[4:8], 0)
		binary.LittleEndian.PutUint32(buf[8:12], 0)
		return buf, nil
	}

	chunkSize := w.chunkSize
	numChunks := (n + chunkSize - 1) / chunkSize

	chunks := make([]ChunkDescriptor, numChunks)
	encodedChunks := make([][]byte, numChunks)

	// Encode each chunk into HCS1
	for i := range numChunks {
		start := i * chunkSize
		end := min(start+chunkSize, n)
		subRecords := records[start:end]

		chunkPayload, err := encodeColumnarSnapshot(subRecords)
		if err != nil {
			return nil, fmt.Errorf("failed to encode snapshot chunk %d: %w", i, err)
		}
		encodedChunks[i] = chunkPayload

		checksum := sha256.Sum256(chunkPayload)
		chunks[i] = ChunkDescriptor{
			Index:         uint32(i),                // #nosec G115 -- numChunks is bounded by record slice length.
			RecordCount:   uint32(len(subRecords)),  // #nosec G115 -- subRecords length is bounded by chunkSize.
			PayloadLength: uint32(len(chunkPayload)), // #nosec G115 -- chunk payload length is bounded.
			Checksum:      checksum,
		}
	}

	// Calculate header size: 4 (magic) + 4 (totalRecords) + 4 (chunkCount) + numChunks * (4 + 4 + 4 + 4 + 32 = 48)
	headerSize := 12 + numChunks*48
	currentOffset := uint32(headerSize) // #nosec G115 -- header size is well within uint32 range.

	for i := range chunks {
		chunks[i].PayloadOffset = currentOffset
		currentOffset += chunks[i].PayloadLength
	}

	totalSize := currentOffset
	buf := make([]byte, totalSize)

	// Write magic and counts
	copy(buf[:4], chunkedSnapshotMagic[:])
	binary.LittleEndian.PutUint32(buf[4:8], uint32(n))          // #nosec G115 -- record count bounded by slice.
	binary.LittleEndian.PutUint32(buf[8:12], uint32(numChunks)) // #nosec G115 -- chunk count bounded by slice.

	// Write chunk descriptors
	off := 12
	for _, c := range chunks {
		binary.LittleEndian.PutUint32(buf[off:off+4], c.Index)
		binary.LittleEndian.PutUint32(buf[off+4:off+8], c.RecordCount)
		binary.LittleEndian.PutUint32(buf[off+8:off+12], c.PayloadOffset)
		binary.LittleEndian.PutUint32(buf[off+12:off+16], c.PayloadLength)
		copy(buf[off+16:off+48], c.Checksum[:])
		off += 48
	}

	// Write chunk payloads
	for i, payload := range encodedChunks {
		chunkOffset := chunks[i].PayloadOffset
		copy(buf[chunkOffset:chunkOffset+chunks[i].PayloadLength], payload)
	}

	return buf, nil
}

// DecodeManifest reads only the header manifest without decoding chunk bodies.
func DecodeManifest(payload []byte) (SnapshotChunkManifest, error) {
	if !isChunkedSnapshot(payload) {
		return SnapshotChunkManifest{}, errors.New("payload is not an HCS2 chunked snapshot")
	}
	if len(payload) < 12 {
		return SnapshotChunkManifest{}, ErrSnapshotCorrupt
	}
	totalRecords := binary.LittleEndian.Uint32(payload[4:8])
	numChunks := binary.LittleEndian.Uint32(payload[8:12])

	expectedHeaderLen := 12 + int(numChunks)*48
	if len(payload) < expectedHeaderLen {
		return SnapshotChunkManifest{}, ErrSnapshotCorrupt
	}

	chunks := make([]ChunkDescriptor, numChunks)
	off := 12
	for i := 0; i < int(numChunks); i++ {
		chunks[i] = ChunkDescriptor{
			Index:         binary.LittleEndian.Uint32(payload[off : off+4]),
			RecordCount:   binary.LittleEndian.Uint32(payload[off+4 : off+8]),
			PayloadOffset: binary.LittleEndian.Uint32(payload[off+8 : off+12]),
			PayloadLength: binary.LittleEndian.Uint32(payload[off+12 : off+16]),
		}
		copy(chunks[i].Checksum[:], payload[off+16:off+48])
		off += 48
	}

	return SnapshotChunkManifest{
		TotalRecords: totalRecords,
		Chunks:       chunks,
	}, nil
}

// decodeChunkedSnapshot streams records from an HCS2 chunked artifact.
// Each chunk is independently verified with SHA256 before decoding.
func decodeChunkedSnapshot(payload []byte, visit database.RecordVisitor) error {
	manifest, err := DecodeManifest(payload)
	if err != nil {
		return err
	}

	for _, chunk := range manifest.Chunks {
		start := int(chunk.PayloadOffset)
		end := start + int(chunk.PayloadLength)
		if start < 0 || end > len(payload) || start > end {
			return ErrSnapshotCorrupt
		}
		chunkBytes := payload[start:end]

		// Verify chunk checksum
		sum := sha256.Sum256(chunkBytes)
		if sum != chunk.Checksum {
			return fmt.Errorf("%w: chunk %d checksum failure", ErrSnapshotCorrupt, chunk.Index)
		}

		if err := decodeColumnarSnapshot(chunkBytes, visit); err != nil {
			return fmt.Errorf("failed to decode chunk %d: %w", chunk.Index, err)
		}
	}

	return nil
}
