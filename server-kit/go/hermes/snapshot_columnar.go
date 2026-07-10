package hermes

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"google.golang.org/protobuf/proto"
)

// Columnar snapshot artifact ("HCS1"): the set-based form of the snapshot
// payload. The row-proto artifact decodes N×F protobuf messages and converts
// every field through recordFromMutation — the measured warm bottleneck
// (~345K allocs / 10K records). This layout stores each column once: field
// names appear once, all text lives in one shared blob decoded as a single
// string whose substrings back every value, and identity columns are
// dictionary-encoded. Decode allocates per column, not per record×field.
//
// Layout (all integers little-endian u32 unless noted):
//
//	magic "HCS1"
//	recordCount
//	dict columns: domain, collection, organization  (dictLen, entries, index/row)
//	plain string column: record_id                  (offsets, blob)
//	createdAt  []int64 (unix nanos, 8 bytes each)
//	updatedAt  []int64
//	vector flag byte; if 1: per row (floatCount, float32 bits...)
//	fieldCount
//	per field: name (string), validity bitmap (ceil(n/8) bytes),
//	           kinds []byte (one per row; 0 when null),
//	           value string column over valid rows (offsets, blob)
//
// The artifact is integrity-guarded by the descriptor checksum like every
// snapshot; the decoder additionally bounds-checks every read so a corrupt
// payload returns ErrSnapshotCorrupt instead of panicking.
var columnarSnapshotMagic = [4]byte{'H', 'C', 'S', '1'}

// isColumnarSnapshot sniffs the artifact format so readers stay compatible
// with row-proto artifacts already in stores (FallbackRefinement: format is an
// implementation detail hidden behind the same warm/compare semantics).
func isColumnarSnapshot(payload []byte) bool {
	return len(payload) >= 4 && [4]byte(payload[:4]) == columnarSnapshotMagic
}

// streamSnapshotRecords decodes any snapshot artifact — columnar HCS1 by magic
// sniff, legacy row-proto otherwise — into a record stream. Both readers
// (WarmFromSnapshot, ShadowCompareSnapshot) go through this one seam so the
// two formats cannot drift semantically.
func streamSnapshotRecords(payload []byte, visit database.RecordVisitor) error {
	if isColumnarSnapshot(payload) {
		return decodeColumnarSnapshot(payload, visit)
	}
	var batch foundationpb.RecordMutationBatch
	if err := proto.Unmarshal(payload, &batch); err != nil {
		return err
	}
	for _, mutation := range batch.Mutations {
		rec, err := recordFromMutation(mutation)
		if err != nil {
			return err
		}
		if err := visit(rec); err != nil {
			return err
		}
	}
	return nil
}

// encodeColumnarSnapshot serializes materialized records into the HCS1 layout.
func encodeColumnarSnapshot(records []database.DomainRecord) ([]byte, error) {
	n := len(records)
	if n > math.MaxUint32 {
		return nil, errors.New("hermes columnar snapshot exceeds u32 record count")
	}
	buf := make([]byte, 0, 64+n*32)
	buf = append(buf, columnarSnapshotMagic[:]...)
	// n <= MaxUint32, validated above.
	buf = appendU32(buf, uint32(n)) // #nosec G115 -- guarded by the record-count check above.

	for _, column := range [][]string{
		collectStrings(records, func(r database.DomainRecord) string { return r.Domain }),
		collectStrings(records, func(r database.DomainRecord) string { return r.Collection }),
		collectStrings(records, func(r database.DomainRecord) string { return r.OrganizationID }),
	} {
		var err error
		if buf, err = appendDictColumn(buf, column); err != nil {
			return nil, err
		}
	}
	buf, err := appendStringColumn(buf, collectStrings(records, func(r database.DomainRecord) string { return r.RecordID }))
	if err != nil {
		return nil, err
	}

	for _, rec := range records {
		buf = binary.LittleEndian.AppendUint64(buf, uint64(rec.CreatedAt.UnixNano()))
	}
	for _, rec := range records {
		buf = binary.LittleEndian.AppendUint64(buf, uint64(rec.UpdatedAt.UnixNano()))
	}

	hasVectors := false
	for _, rec := range records {
		if len(rec.Vector) > 0 {
			hasVectors = true
			break
		}
	}
	if hasVectors {
		buf = append(buf, 1)
		for _, rec := range records {
			floats, err := u32Len(len(rec.Vector), "vector")
			if err != nil {
				return nil, err
			}
			buf = appendU32(buf, floats)
			for _, f := range rec.Vector {
				buf = binary.LittleEndian.AppendUint32(buf, math.Float32bits(f))
			}
		}
	} else {
		buf = append(buf, 0)
	}

	// Field union in first-seen order for deterministic output.
	fieldIndex := map[string]int{}
	fieldNames := []string{}
	for _, rec := range records {
		for _, field := range rec.Data {
			if _, ok := fieldIndex[field.Name]; !ok {
				fieldIndex[field.Name] = len(fieldNames)
				fieldNames = append(fieldNames, field.Name)
			}
		}
	}
	fieldCount, err := u32Len(len(fieldNames), "field set")
	if err != nil {
		return nil, err
	}
	buf = appendU32(buf, fieldCount)

	kinds := make([]byte, n)
	texts := make([]string, 0, n)
	validity := make([]byte, (n+7)/8)
	for _, name := range fieldNames {
		if buf, err = appendString(buf, name); err != nil {
			return nil, err
		}
		for i := range kinds {
			kinds[i] = 0
		}
		for i := range validity {
			validity[i] = 0
		}
		texts = texts[:0]
		for i, rec := range records {
			if value, ok := rec.Data.Get(name); ok {
				validity[i/8] |= 1 << (i % 8)
				kinds[i] = byte(value.Kind)
				texts = append(texts, value.Text)
			}
		}
		buf = append(buf, validity...)
		buf = append(buf, kinds...)
		if buf, err = appendStringColumn(buf, texts); err != nil {
			return nil, err
		}
	}
	return buf, nil
}

// decodeColumnarSnapshot streams records out of an HCS1 payload. Every text
// value is a substring of one shared blob string, so decode cost scales with
// columns, not records×fields.
func decodeColumnarSnapshot(payload []byte, visit database.RecordVisitor) error {
	c := &columnarCursor{buf: payload}
	if magic := c.bytes(4); c.err != nil || [4]byte(magic) != columnarSnapshotMagic {
		return fmt.Errorf("%w: bad columnar snapshot magic", ErrSnapshotCorrupt)
	}
	n := int(c.u32())
	domains := c.dictColumn(n)
	collections := c.dictColumn(n)
	organizations := c.dictColumn(n)
	recordIDs := c.stringColumn(n)
	createdAt := c.int64Column(n)
	updatedAt := c.int64Column(n)

	var vectors [][]float32
	if flag := c.bytes(1); c.err == nil && len(flag) == 1 && flag[0] == 1 {
		vectors = make([][]float32, n)
		for i := 0; i < n && c.err == nil; i++ {
			floats := int(c.u32())
			if floats == 0 {
				continue
			}
			raw := c.bytes(floats * 4)
			if c.err != nil {
				break
			}
			vec := make([]float32, floats)
			for j := range vec {
				vec[j] = math.Float32frombits(binary.LittleEndian.Uint32(raw[j*4:]))
			}
			vectors[i] = vec
		}
	}

	fieldCount := int(c.u32())
	type fieldColumn struct {
		name     string
		validity []byte
		kinds    []byte
		texts    []string
	}
	fields := make([]fieldColumn, 0, fieldCount)
	for f := 0; f < fieldCount && c.err == nil; f++ {
		name := c.str()
		validity := c.bytes((n + 7) / 8)
		kinds := c.bytes(n)
		valid := 0
		for i := range n {
			if len(validity) > i/8 && validity[i/8]&(1<<(i%8)) != 0 {
				valid++
			}
		}
		fields = append(fields, fieldColumn{name: name, validity: validity, kinds: kinds, texts: c.stringColumn(valid)})
	}
	if c.err != nil {
		return fmt.Errorf("%w: %v", ErrSnapshotCorrupt, c.err)
	}

	cursorPerField := make([]int, len(fields))
	for i := range n {
		data := make(database.RecordData, 0, len(fields))
		for f := range fields {
			col := &fields[f]
			if len(col.validity) <= i/8 || col.validity[i/8]&(1<<(i%8)) == 0 {
				continue
			}
			text := col.texts[cursorPerField[f]]
			cursorPerField[f]++
			data = append(data, database.RecordField{
				Name:  col.name,
				Value: database.RecordValue{Kind: database.RecordValueKind(col.kinds[i]), Text: text},
			})
		}
		rec := database.DomainRecord{
			Domain:         domains[i],
			Collection:     collections[i],
			OrganizationID: organizations[i],
			RecordID:       recordIDs[i],
			Data:           data,
			CreatedAt:      time.Unix(0, createdAt[i]).UTC(),
			UpdatedAt:      time.Unix(0, updatedAt[i]).UTC(),
		}
		if vectors != nil {
			rec.Vector = vectors[i]
		}
		if err := visit(rec); err != nil {
			return err
		}
	}
	return nil
}

// --- encoding primitives ---

func appendU32(buf []byte, v uint32) []byte {
	return binary.LittleEndian.AppendUint32(buf, v)
}

// u32Len guards every int→u32 length conversion in the encoder: the wire
// format is u32-addressed, so oversized inputs must fail as controlled errors
// rather than truncate silently.
func u32Len(n int, what string) (uint32, error) {
	if n < 0 || n > math.MaxUint32 {
		return 0, fmt.Errorf("hermes columnar snapshot %s length %d exceeds u32", what, n)
	}
	return uint32(n), nil
}

func appendString(buf []byte, s string) ([]byte, error) {
	size, err := u32Len(len(s), "string")
	if err != nil {
		return nil, err
	}
	buf = appendU32(buf, size)
	return append(buf, s...), nil
}

// appendStringColumn writes offsets (prefix sums) plus one shared blob.
func appendStringColumn(buf []byte, values []string) ([]byte, error) {
	total := 0
	for _, s := range values {
		total += len(s)
	}
	totalSize, err := u32Len(total, "column blob")
	if err != nil {
		return nil, err
	}
	count, err := u32Len(len(values), "column")
	if err != nil {
		return nil, err
	}
	buf = appendU32(buf, count)
	offset := uint32(0)
	for _, s := range values {
		// Every cumulative offset is <= total, validated above.
		offset += uint32(len(s)) // #nosec G115 -- bounded by the column-blob u32Len guard.
		buf = appendU32(buf, offset)
	}
	buf = appendU32(buf, totalSize)
	for _, s := range values {
		buf = append(buf, s...)
	}
	return buf, nil
}

// appendDictColumn dictionary-encodes low-cardinality columns (domain,
// collection, organization) so repeated values are stored once.
func appendDictColumn(buf []byte, values []string) ([]byte, error) {
	if _, err := u32Len(len(values), "dict rows"); err != nil {
		return nil, err
	}
	dict := []string{}
	index := map[string]uint32{}
	rows := make([]uint32, len(values))
	for i, s := range values {
		at, ok := index[s]
		if !ok {
			// Dict cardinality <= row count, validated above.
			at = uint32(len(dict)) // #nosec G115 -- bounded by the dict-rows u32Len guard.
			index[s] = at
			dict = append(dict, s)
		}
		rows[i] = at
	}
	buf, err := appendStringColumn(buf, dict)
	if err != nil {
		return nil, err
	}
	for _, at := range rows {
		buf = appendU32(buf, at)
	}
	return buf, nil
}

func collectStrings(records []database.DomainRecord, get func(database.DomainRecord) string) []string {
	out := make([]string, len(records))
	for i := range records {
		out[i] = get(records[i])
	}
	return out
}

// --- decoding primitives (bounds-checked cursor) ---

type columnarCursor struct {
	buf []byte
	at  int
	err error
}

func (c *columnarCursor) bytes(n int) []byte {
	if c.err != nil {
		return nil
	}
	if n < 0 || c.at+n > len(c.buf) {
		c.err = errors.New("columnar snapshot truncated")
		return nil
	}
	out := c.buf[c.at : c.at+n]
	c.at += n
	return out
}

func (c *columnarCursor) u32() uint32 {
	raw := c.bytes(4)
	if c.err != nil {
		return 0
	}
	return binary.LittleEndian.Uint32(raw)
}

func (c *columnarCursor) str() string {
	n := int(c.u32())
	raw := c.bytes(n)
	if c.err != nil {
		return ""
	}
	return string(raw)
}

// stringColumn reads offsets plus blob and returns substrings of ONE shared
// string — the allocation shape the columnar artifact exists for.
func (c *columnarCursor) stringColumn(expect int) []string {
	out := c.stringColumnAny()
	if c.err != nil {
		return nil
	}
	if len(out) != expect {
		c.err = fmt.Errorf("columnar string column count %d, want %d", len(out), expect)
		return nil
	}
	return out
}

func (c *columnarCursor) stringColumnAny() []string {
	count := int(c.u32())
	if c.err != nil {
		return nil
	}
	if count < 0 || count > len(c.buf) {
		c.err = errors.New("columnar string column count out of range")
		return nil
	}
	offsets := make([]uint32, count)
	for i := range offsets {
		offsets[i] = c.u32()
	}
	blobLen := int(c.u32())
	raw := c.bytes(blobLen)
	if c.err != nil {
		return nil
	}
	if count > 0 && int(offsets[count-1]) != blobLen {
		c.err = errors.New("columnar string column offsets disagree with blob")
		return nil
	}
	blob := string(raw)
	out := make([]string, count)
	start := uint32(0)
	for i, end := range offsets {
		if end < start || int(end) > blobLen {
			c.err = errors.New("columnar string column offset out of range")
			return nil
		}
		out[i] = blob[start:end]
		start = end
	}
	return out
}

func (c *columnarCursor) dictColumn(n int) []string {
	dict := c.stringColumnAny()
	if c.err != nil {
		return nil
	}
	out := make([]string, n)
	for i := range n {
		at := int(c.u32())
		if c.err != nil {
			return nil
		}
		if at >= len(dict) {
			c.err = errors.New("columnar dict index out of range")
			return nil
		}
		out[i] = dict[at]
	}
	return out
}

func (c *columnarCursor) int64Column(n int) []int64 {
	raw := c.bytes(n * 8)
	if c.err != nil {
		return nil
	}
	out := make([]int64, n)
	for i := range out {
		// Deliberate two's-complement round-trip: encode wrote int64 UnixNano
		// through uint64; this restores the original signed value bit-for-bit.
		out[i] = int64(binary.LittleEndian.Uint64(raw[i*8:])) // #nosec G115 -- int64 timestamp round-trip, not a range conversion.
	}
	return out
}
