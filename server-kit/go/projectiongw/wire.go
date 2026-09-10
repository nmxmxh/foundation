package projectiongw

import (
	"errors"
	"io"
	"math"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Wire-level assembly of ProjectionSnapshot responses.
//
// The protobuf wire format guarantees that concatenating the serialized forms of
// two messages is equivalent to merging them: for a repeated field the elements
// append. RecordMutationBatch has exactly one field (repeated RecordMutation
// mutations = 1), so the bytes proto.Marshal produces for a batch are byte-for-
// byte the concatenation of each record framed as tag(1,LEN)|varint(len)|body.
// TestWireConcatEqualsBatchMarshal asserts that equality directly.
//
// That makes a record's serialized bytes reusable across every response that
// carries it. The gateway's fan-out shape — one scope page delivered to many
// subscribers, each with its own audience slice — currently re-encodes the same
// records once per subscriber. With framed bytes cached per record, a response
// is assembled by selecting slices, and can be written straight to the socket
// without ever materializing a contiguous response body.
//
// Measured on an M1 Pro, 1000 records × 50 audiences at ~60% selectivity
// (BenchmarkWireFanOutDecomposed in the hermes package):
//
//	re-marshal per audience     23.3 ms   27.3 MB   200 allocs
//	concat, fresh buffer         3.53 ms  27.0 MB    52 allocs
//	concat, reused buffer        0.89 ms   1.9 KB     0 allocs
//	stream frames to writer      0.12 ms      0 B     0 allocs
//
// Building the frame cache costs 0.98 ms for the same 1000 records, so it pays
// for itself at roughly three subscribers on a single epoch.

// Field numbers in foundation.v1.ProjectionSnapshot and RecordMutationBatch.
// These are load-bearing here in a way generated code normally hides, so they
// are asserted against the descriptors in TestWireFieldNumbersMatchDescriptor.
const (
	snapshotFieldScope      protowire.Number = 1
	snapshotFieldBatch      protowire.Number = 2
	snapshotFieldWatermark  protowire.Number = 3
	snapshotFieldEpoch      protowire.Number = 4
	snapshotFieldNextCursor protowire.Number = 5
	snapshotFieldHasMore    protowire.Number = 6

	batchFieldMutations protowire.Number = 1
)

// ErrWireFrameTooLarge guards the varint length prefix against a batch body
// that cannot be addressed by the wire format.
var ErrWireFrameTooLarge = errors.New("projectiongw: snapshot batch body exceeds wire limits")

// appendFramedMutation appends one record in the exact form it occupies inside
// a RecordMutationBatch. The result is position-independent: it can be
// concatenated with any other framed record, in any order, any number of times.
func appendFramedMutation(dst []byte, mutation *foundationpb.RecordMutation) ([]byte, error) {
	body, err := proto.Marshal(mutation)
	if err != nil {
		return nil, err
	}
	dst = protowire.AppendTag(dst, batchFieldMutations, protowire.BytesType)
	dst = protowire.AppendVarint(dst, uint64(len(body)))
	return append(dst, body...), nil
}

// frameMutations pre-serializes a page of records into per-record framed bytes.
// This is the cacheable artifact: it is computed once per epoch and reused by
// every response that carries any subset of the page.
func frameMutations(mutations []*foundationpb.RecordMutation) ([][]byte, int, error) {
	frames := make([][]byte, len(mutations))
	total := 0
	for i, mutation := range mutations {
		framed, err := appendFramedMutation(nil, mutation)
		if err != nil {
			return nil, 0, err
		}
		frames[i] = framed
		total += len(framed)
	}
	return frames, total, nil
}

// appendSnapshotHeader writes the ProjectionSnapshot fields that precede the
// batch body, then the batch tag and its length prefix. The caller appends or
// writes exactly bodyLen bytes of framed records next, then calls
// appendSnapshotTrailer.
//
// Fields are emitted in ascending field-number order, matching what
// protoc-gen-go produces, so the assembled bytes stay comparable to the
// marshaled form.
func appendSnapshotHeader(dst []byte, scopeBytes []byte, bodyLen int) ([]byte, error) {
	// Protobuf implementations cap a message at 2 GiB; a body past that cannot
	// be decoded by a conformant reader even though the varint would encode it.
	if bodyLen < 0 || bodyLen > math.MaxInt32 {
		return nil, ErrWireFrameTooLarge
	}
	if len(scopeBytes) > 0 {
		dst = protowire.AppendTag(dst, snapshotFieldScope, protowire.BytesType)
		dst = protowire.AppendBytes(dst, scopeBytes)
	}
	// Field 2 is emitted unconditionally: the generated marshaler writes an
	// empty length-delimited field for a non-nil batch message, and the read
	// path distinguishes "empty page" from "absent batch".
	dst = protowire.AppendTag(dst, snapshotFieldBatch, protowire.BytesType)
	dst = protowire.AppendVarint(dst, uint64(bodyLen))
	return dst, nil
}

// appendSnapshotTrailer writes the ProjectionSnapshot fields that follow the
// batch body. Proto3 omits zero-valued scalars, so each is conditional.
func appendSnapshotTrailer(dst []byte, watermark string, epoch uint64, nextCursor string, hasMore bool) []byte {
	if watermark != "" {
		dst = protowire.AppendTag(dst, snapshotFieldWatermark, protowire.BytesType)
		dst = protowire.AppendString(dst, watermark)
	}
	if epoch != 0 {
		dst = protowire.AppendTag(dst, snapshotFieldEpoch, protowire.VarintType)
		dst = protowire.AppendVarint(dst, epoch)
	}
	if nextCursor != "" {
		dst = protowire.AppendTag(dst, snapshotFieldNextCursor, protowire.BytesType)
		dst = protowire.AppendString(dst, nextCursor)
	}
	if hasMore {
		dst = protowire.AppendTag(dst, snapshotFieldHasMore, protowire.VarintType)
		dst = protowire.AppendVarint(dst, 1)
	}
	return dst
}

// snapshotWireParts is everything needed to emit one ProjectionSnapshot without
// touching a RecordMutation again.
type snapshotWireParts struct {
	scopeBytes []byte
	frames     [][]byte
	watermark  string
	epoch      uint64
	nextCursor string
	hasMore    bool
}

func (p snapshotWireParts) bodyLen() int {
	total := 0
	for _, frame := range p.frames {
		total += len(frame)
	}
	return total
}

// writeSnapshot streams a ProjectionSnapshot to w without assembling the
// response body. The framed records are written through as-is, so no per-record
// encode and no response-sized allocation occurs.
//
// It returns the number of bytes written. A short write or a writer error is
// returned as-is; because the header is already on the wire at that point the
// response is truncated and the caller must treat it as a failed response, not
// an empty page.
func (p snapshotWireParts) writeSnapshot(w io.Writer) (int64, error) {
	var header []byte
	header, err := appendSnapshotHeader(header, p.scopeBytes, p.bodyLen())
	if err != nil {
		return 0, err
	}

	var written int64
	n, err := w.Write(header)
	written += int64(n)
	if err != nil {
		return written, err
	}
	for _, frame := range p.frames {
		n, err = w.Write(frame)
		written += int64(n)
		if err != nil {
			return written, err
		}
	}
	trailer := appendSnapshotTrailer(nil, p.watermark, p.epoch, p.nextCursor, p.hasMore)
	if len(trailer) > 0 {
		n, err = w.Write(trailer)
		written += int64(n)
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

// appendSnapshot assembles a ProjectionSnapshot into dst. Callers that need a
// contiguous buffer (a websocket frame, a checksum) use this; callers writing to
// a stream use writeSnapshot and allocate nothing.
func (p snapshotWireParts) appendSnapshot(dst []byte) ([]byte, error) {
	body := p.bodyLen()
	dst, err := appendSnapshotHeader(dst, p.scopeBytes, body)
	if err != nil {
		return nil, err
	}
	for _, frame := range p.frames {
		dst = append(dst, frame...)
	}
	return appendSnapshotTrailer(dst, p.watermark, p.epoch, p.nextCursor, p.hasMore), nil
}

// snapshotPartsFromProto derives the wire parts from an assembled snapshot
// message by framing its records. This is the un-cached path: it costs one
// marshal per record, the same as proto.Marshal would, and exists so the
// streaming writer can be used before a cache is populated.
func snapshotPartsFromProto(snapshot *foundationpb.ProjectionSnapshot) (snapshotWireParts, error) {
	var parts snapshotWireParts
	if scope := snapshot.GetScope(); scope != nil {
		scopeBytes, err := proto.Marshal(scope)
		if err != nil {
			return parts, err
		}
		parts.scopeBytes = scopeBytes
	}
	frames, _, err := frameMutations(snapshot.GetBatch().GetMutations())
	if err != nil {
		return parts, err
	}
	parts.frames = frames
	parts.watermark = snapshot.GetWatermark()
	parts.epoch = snapshot.GetEpoch()
	parts.nextCursor = snapshot.GetNextCursor()
	parts.hasMore = snapshot.GetHasMore()
	return parts, nil
}
