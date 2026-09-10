package hermes

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Wire-level properties of the foundation.v1 projection messages, and the
// partial-decode wins they make available.
//
// The fan-out property (concatenation of framed records equals marshaling the
// batch) is exploited in production by projectiongw and is asserted there, end
// to end, against the real HTTP handler. What remains here is the read side:
// a length-prefixed skip is O(fields) rather than O(bytes), and a packed
// repeated float is already the contiguous little-endian F32 run a columnar
// reader wants. Neither has a production caller yet — the hermes/runtimehost
// columnar bridge is where they belong — so these exist to record the size of
// the prize and to pin the encoding assumptions it rests on.

const (
	fieldNumRecordVector  = 9  // RecordMutation.vector
	fieldNumRecordPayload = 12 // RecordMutation.payload
	fieldNumRecordVersion = 3  // RecordMutation.version
	fieldNumRecordID      = 7  // RecordMutation.record_id
)

// wireBenchRecord builds a mutation shaped like a realistic projection record:
// identity strings, three scalar fields, a dense vector, and an opaque payload.
func wireBenchRecord(tb testing.TB, index, vectorDim, payloadBytes int) *foundationpb.RecordMutation {
	tb.Helper()
	vector := make([]float32, vectorDim)
	for j := range vector {
		vector[j] = float32(index*vectorDim+j) * 0.5
	}
	payload := make([]byte, payloadBytes)
	for j := range payload {
		payload[j] = byte((index + j) % 256) // #nosec G115 -- masked to a byte
	}
	return &foundationpb.RecordMutation{
		Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
		Version:        uint64(max(index, 0)) + 1, // #nosec G115 -- index is a loop counter
		Domain:         "signals",
		Collection:     "ticks",
		OrganizationId: "org_00000000000000000001",
		RecordId:       fmt.Sprintf("tick_%08d", index),
		Fields: []*foundationpb.FieldValue{
			{Name: "symbol", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_StringValue{StringValue: "OVS"}}},
			{Name: "price", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_DoubleValue{DoubleValue: float64(index) * 1.5}}},
			{Name: "active", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_BoolValue{BoolValue: index%2 == 0}}},
		},
		Vector:  vector,
		Payload: payload,
	}
}

func mustMarshalRecord(tb testing.TB, mutation *foundationpb.RecordMutation) []byte {
	tb.Helper()
	raw, err := proto.Marshal(mutation)
	if err != nil {
		tb.Fatalf("marshal mutation: %v", err)
	}
	return raw
}

// findFieldBytes walks top-level tags and returns the raw, length-delimited body
// of the first occurrence of fieldNum. A skipped field costs one varint read plus
// a length skip — never a decode of its contents — which is why this is
// independent of how large the skipped fields are.
func findFieldBytes(raw []byte, fieldNum protowire.Number) []byte {
	for len(raw) > 0 {
		num, typ, tagLen := protowire.ConsumeTag(raw)
		if tagLen < 0 {
			return nil
		}
		raw = raw[tagLen:]
		if num == fieldNum && typ == protowire.BytesType {
			body, bodyLen := protowire.ConsumeBytes(raw)
			if bodyLen < 0 {
				return nil
			}
			return body
		}
		skipped := protowire.ConsumeFieldValue(num, typ, raw)
		if skipped < 0 {
			return nil
		}
		raw = raw[skipped:]
	}
	return nil
}

// wireIdentity extracts record_id and version without decoding the vector, the
// payload, or the field list.
func wireIdentity(raw []byte) ([]byte, uint64) {
	var id []byte
	var version uint64
	for len(raw) > 0 {
		num, typ, tagLen := protowire.ConsumeTag(raw)
		if tagLen < 0 {
			return nil, 0
		}
		raw = raw[tagLen:]
		switch {
		case num == fieldNumRecordID && typ == protowire.BytesType:
			body, bodyLen := protowire.ConsumeBytes(raw)
			if bodyLen < 0 {
				return nil, 0
			}
			id = body
			raw = raw[bodyLen:]
		case num == fieldNumRecordVersion && typ == protowire.VarintType:
			value, valueLen := protowire.ConsumeVarint(raw)
			if valueLen < 0 {
				return nil, 0
			}
			version = value
			raw = raw[valueLen:]
		default:
			skipped := protowire.ConsumeFieldValue(num, typ, raw)
			if skipped < 0 {
				return nil, 0
			}
			raw = raw[skipped:]
		}
		if id != nil && version != 0 {
			return id, version
		}
	}
	return id, version
}

// decodePackedF32 bulk-decodes a packed fixed32 run into dst (reused when it has
// capacity). No protobuf reflection, no per-element tag handling.
//
// It stops short of reinterpreting the bytes as a []float32 outright: protobuf
// gives no alignment guarantee for where a packed run starts, so an unsafe cast
// would be unsound even though the byte layout is otherwise identical.
func decodePackedF32(packed []byte, dst []float32) []float32 {
	n := len(packed) / 4
	if cap(dst) < n {
		dst = make([]float32, n)
	}
	dst = dst[:n]
	for i := range dst {
		dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(packed[i*4:]))
	}
	return dst
}

// --- Encoding properties the partial-decode paths depend on ---

// TestWireBytesFieldIsCopied documents whether proto.Unmarshal aliases the input
// buffer for a bytes field. The answer decides whether a zero-copy payload path
// needs protowire at all: it does.
func TestWireBytesFieldIsCopied(t *testing.T) {
	original := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	raw, err := proto.Marshal(&foundationpb.RecordMutation{RecordId: "r1", Payload: original})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded foundationpb.RecordMutation
	if unmarshalErr := proto.Unmarshal(raw, &decoded); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	before := append([]byte(nil), decoded.GetPayload()...)

	// Scribble over the entire source buffer.
	for i := range raw {
		raw[i] = 0xFF
	}
	if !bytes.Equal(decoded.GetPayload(), before) {
		t.Fatal("generated decoder aliases bytes fields; the partial-decode paths assume it copies")
	}

	// protowire, by contrast, is documented to return a sub-slice of the input,
	// which is what makes a zero-copy read possible at all.
	raw2, err := proto.Marshal(&foundationpb.RecordMutation{RecordId: "r1", Payload: original})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	aliased := findFieldBytes(raw2, fieldNumRecordPayload)
	if aliased == nil {
		t.Fatal("payload field not found via protowire")
	}
	raw2[len(raw2)-1] = 0x7F
	if aliased[len(aliased)-1] != 0x7F {
		t.Fatal("protowire ConsumeBytes did not alias the input buffer")
	}
}

// TestWirePackedFloatLayout asserts the packed encoding of repeated float is
// bit-identical to the little-endian F32 run a columnar reader wants, and that
// the bulk fast path agrees with the generated decoder.
func TestWirePackedFloatLayout(t *testing.T) {
	vector := []float32{0, 1, -1, 3.5, math.MaxFloat32, math.SmallestNonzeroFloat32, 1e-8, -2.25}
	raw, err := proto.Marshal(&foundationpb.RecordMutation{RecordId: "r1", Vector: vector})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	packed := findFieldBytes(raw, fieldNumRecordVector)
	if packed == nil {
		t.Fatal("vector field is not length-delimited; the bulk path must fall back to the generated decoder")
	}
	if len(packed) != len(vector)*4 {
		t.Fatalf("packed run = %d bytes, want %d", len(packed), len(vector)*4)
	}
	for i, want := range vector {
		got := math.Float32frombits(binary.LittleEndian.Uint32(packed[i*4:]))
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("element %d = %v, want %v", i, got, want)
		}
	}

	fast := decodePackedF32(packed, nil)
	var decoded foundationpb.RecordMutation
	if err := proto.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(fast) != len(decoded.GetVector()) {
		t.Fatalf("fast len = %d, generated len = %d", len(fast), len(decoded.GetVector()))
	}
	for i := range fast {
		if math.Float32bits(fast[i]) != math.Float32bits(decoded.GetVector()[i]) {
			t.Fatalf("element %d: fast %v != generated %v", i, fast[i], decoded.GetVector()[i])
		}
	}
}

// TestWireIdentityMatchesGeneratedDecode pins the identity fast path to the
// generated decoder.
func TestWireIdentityMatchesGeneratedDecode(t *testing.T) {
	mutation := wireBenchRecord(t, 7, 64, 512)
	raw := mustMarshalRecord(t, mutation)

	id, version := wireIdentity(raw)
	if string(id) != mutation.GetRecordId() {
		t.Fatalf("record_id = %q, want %q", id, mutation.GetRecordId())
	}
	if version != mutation.GetVersion() {
		t.Fatalf("version = %d, want %d", version, mutation.GetVersion())
	}
}

// --- Benchmarks ---

// benchVectorRead measures one strategy for reading the dense vector out of a
// serialized mutation.
func benchVectorRead(b *testing.B, raw []byte, dim int, read func(raw []byte, dst []float32) int) {
	b.Helper()
	dst := make([]float32, 0, dim)
	b.ReportAllocs()
	for b.Loop() {
		if got := read(raw, dst); got != dim {
			b.Fatalf("read %d elements, want %d", got, dim)
		}
	}
}

// BenchmarkWireVectorOnlyRead compares reading just the dense vector: a full
// generated decode, a length-prefixed skip plus bulk fixed32 decode, and the
// skip alone (which is what a consumer that can work on the packed bytes pays).
//
// The skip-only cost is flat across dimensions — that is the O(fields) property
// made visible.
func BenchmarkWireVectorOnlyRead(b *testing.B) {
	for _, dim := range []int{128, 768, 1536} {
		raw := mustMarshalRecord(b, wireBenchRecord(b, 0, dim, 4096))

		b.Run(fmt.Sprintf("dim%d/GeneratedUnmarshal", dim), func(b *testing.B) {
			benchVectorRead(b, raw, dim, func(raw []byte, _ []float32) int {
				var decoded foundationpb.RecordMutation
				if err := proto.Unmarshal(raw, &decoded); err != nil {
					return -1
				}
				return len(decoded.GetVector())
			})
		})

		b.Run(fmt.Sprintf("dim%d/WireSkipBulkDecode", dim), func(b *testing.B) {
			benchVectorRead(b, raw, dim, func(raw []byte, dst []float32) int {
				return len(decodePackedF32(findFieldBytes(raw, fieldNumRecordVector), dst))
			})
		})

		b.Run(fmt.Sprintf("dim%d/WireSkipAliasOnly", dim), func(b *testing.B) {
			benchVectorRead(b, raw, dim, func(raw []byte, _ []float32) int {
				return len(findFieldBytes(raw, fieldNumRecordVector)) / 4
			})
		})
	}
}

// BenchmarkWireRecordOnlyRead is the inverse: reading identity and version while
// skipping a 1536-float vector and a 4 KB payload entirely.
func BenchmarkWireRecordOnlyRead(b *testing.B) {
	raw := mustMarshalRecord(b, wireBenchRecord(b, 0, 1536, 4096))

	b.Run("GeneratedUnmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var decoded foundationpb.RecordMutation
			if err := proto.Unmarshal(raw, &decoded); err != nil {
				b.Fatalf("unmarshal: %v", err)
			}
			if decoded.GetRecordId() == "" || decoded.GetVersion() == 0 {
				b.Fatal("missing identity")
			}
		}
	})

	b.Run("WireSkipIdentityOnly", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			id, version := wireIdentity(raw)
			if id == nil || version == 0 {
				b.Fatal("missing identity")
			}
		}
	})
}

// BenchmarkWireMarshalAppend measures per-message encode with a fresh allocation
// versus MarshalAppend into a reused buffer. The win is one allocation, which
// only pays where the caller can own the buffer.
func BenchmarkWireMarshalAppend(b *testing.B) {
	mutation := wireBenchRecord(b, 0, 128, 256)

	b.Run("ProtoMarshal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			raw, err := proto.Marshal(mutation)
			if err != nil {
				b.Fatalf("marshal: %v", err)
			}
			if len(raw) == 0 {
				b.Fatal("empty")
			}
		}
	})

	b.Run("MarshalAppendReusedBuffer", func(b *testing.B) {
		options := proto.MarshalOptions{}
		buf := make([]byte, 0, options.Size(mutation))
		b.ReportAllocs()
		for b.Loop() {
			var err error
			buf, err = options.MarshalAppend(buf[:0], mutation)
			if err != nil {
				b.Fatalf("marshal append: %v", err)
			}
			if len(buf) == 0 {
				b.Fatal("empty")
			}
		}
	})
}
