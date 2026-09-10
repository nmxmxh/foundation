package projectiongw

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	foundationpb "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/generated/foundation/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func wireTestMutations(n int, vectorDim int) []*foundationpb.RecordMutation {
	mutations := make([]*foundationpb.RecordMutation, n)
	for i := range mutations {
		var vector []float32
		if vectorDim > 0 {
			vector = make([]float32, vectorDim)
			for j := range vector {
				vector[j] = float32(i*vectorDim+j) * 0.25
			}
		}
		mutations[i] = &foundationpb.RecordMutation{
			Operation:      foundationpb.ProjectionOperation_PROJECTION_OPERATION_UPSERT,
			Version:        uint64(i + 1),
			Domain:         "signals",
			Collection:     "ticks",
			OrganizationId: "org_1",
			RecordId:       fmt.Sprintf("tick_%05d", i),
			Fields: []*foundationpb.FieldValue{
				{Name: "symbol", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_StringValue{StringValue: "OVS"}}},
				{Name: "price", Value: &foundationpb.ScalarValue{Kind: &foundationpb.ScalarValue_DoubleValue{DoubleValue: float64(i) * 1.5}}},
			},
			Vector:  vector,
			Payload: []byte{byte(i), byte(i + 1), byte(i + 2)},
		}
	}
	return mutations
}

// TestWireFieldNumbersMatchDescriptor pins the hand-written field numbers to the
// generated descriptors. Renumbering a field in projection.proto without
// updating wire.go would otherwise produce a response that decodes into the
// wrong fields silently, which is exactly the drift the contract checks exist to
// prevent.
func TestWireFieldNumbersMatchDescriptor(t *testing.T) {
	snapshotFields := (&foundationpb.ProjectionSnapshot{}).ProtoReflect().Descriptor().Fields()
	for name, want := range map[protoreflect.Name]protowire.Number{
		"scope":       snapshotFieldScope,
		"batch":       snapshotFieldBatch,
		"watermark":   snapshotFieldWatermark,
		"epoch":       snapshotFieldEpoch,
		"next_cursor": snapshotFieldNextCursor,
		"has_more":    snapshotFieldHasMore,
	} {
		field := snapshotFields.ByName(name)
		if field == nil {
			t.Fatalf("ProjectionSnapshot has no field %q", name)
		}
		if field.Number() != want {
			t.Fatalf("ProjectionSnapshot.%s number = %d, wire.go expects %d", name, field.Number(), want)
		}
	}

	batchFields := (&foundationpb.RecordMutationBatch{}).ProtoReflect().Descriptor().Fields()
	if batchFields.Len() != 1 {
		t.Fatalf("RecordMutationBatch has %d fields; concatenation-as-merge assembly assumes exactly one repeated field", batchFields.Len())
	}
	mutations := batchFields.ByName("mutations")
	if mutations == nil {
		t.Fatal("RecordMutationBatch has no field \"mutations\"")
	}
	if mutations.Number() != batchFieldMutations {
		t.Fatalf("RecordMutationBatch.mutations number = %d, wire.go expects %d", mutations.Number(), batchFieldMutations)
	}
	if !mutations.IsList() {
		t.Fatal("RecordMutationBatch.mutations is not repeated; framed-record concatenation would overwrite instead of append")
	}
}

// TestWireAppendSnapshotMatchesMarshal is the equivalence the whole optimization
// rests on: the assembled bytes must decode to the same message the generated
// marshaler would have produced.
func TestWireAppendSnapshotMatchesMarshal(t *testing.T) {
	for _, tc := range []struct {
		name       string
		records    int
		vectorDim  int
		scope      *foundationpb.ProjectionScope
		watermark  string
		epoch      uint64
		nextCursor string
		hasMore    bool
	}{
		{name: "typical page", records: 24, vectorDim: 8, scope: &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"}, watermark: "128", epoch: 9, nextCursor: "64", hasMore: true},
		{name: "empty page", records: 0, scope: &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"}, watermark: "0", epoch: 1},
		{name: "no scope", records: 3},
		{name: "zero epoch and no cursor", records: 5, vectorDim: 4, scope: &foundationpb.ProjectionScope{TenantId: "org_2", Domain: "d", Collection: "c"}},
		{name: "dense vectors", records: 8, vectorDim: 1536, scope: &foundationpb.ProjectionScope{TenantId: "org_3", Domain: "d", Collection: "c"}, watermark: "7", epoch: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutations := wireTestMutations(tc.records, tc.vectorDim)
			want := &foundationpb.ProjectionSnapshot{
				Scope:      tc.scope,
				Batch:      &foundationpb.RecordMutationBatch{Mutations: mutations},
				Watermark:  tc.watermark,
				Epoch:      tc.epoch,
				NextCursor: tc.nextCursor,
				HasMore:    tc.hasMore,
			}
			wantRaw, err := proto.Marshal(want)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			parts, err := snapshotPartsFromProto(want)
			if err != nil {
				t.Fatalf("parts: %v", err)
			}
			gotRaw, err := parts.appendSnapshot(nil)
			if err != nil {
				t.Fatalf("append: %v", err)
			}

			// Byte equality is the strong form and holds here because both paths
			// emit ascending field order over the same values.
			if !bytes.Equal(wantRaw, gotRaw) {
				t.Errorf("assembled bytes differ from marshal (%d vs %d bytes)", len(wantRaw), len(gotRaw))
			}

			var got foundationpb.ProjectionSnapshot
			if err := proto.Unmarshal(gotRaw, &got); err != nil {
				t.Fatalf("unmarshal assembled: %v", err)
			}
			if !proto.Equal(want, &got) {
				t.Fatal("assembled snapshot is not proto-equal to the source")
			}
		})
	}
}

// TestWireWriteSnapshotMatchesAppend confirms the streaming writer produces the
// same bytes as the buffered assembly, so the two paths cannot diverge.
func TestWireWriteSnapshotMatchesAppend(t *testing.T) {
	snapshot := &foundationpb.ProjectionSnapshot{
		Scope:      &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"},
		Batch:      &foundationpb.RecordMutationBatch{Mutations: wireTestMutations(37, 16)},
		Watermark:  "512",
		Epoch:      11,
		NextCursor: "256",
		HasMore:    true,
	}
	parts, err := snapshotPartsFromProto(snapshot)
	if err != nil {
		t.Fatalf("parts: %v", err)
	}
	appended, err := parts.appendSnapshot(nil)
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	var sink bytes.Buffer
	written, err := parts.writeSnapshot(&sink)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if written != int64(sink.Len()) {
		t.Fatalf("reported %d bytes written, buffer holds %d", written, sink.Len())
	}
	if !bytes.Equal(appended, sink.Bytes()) {
		t.Fatalf("streamed bytes differ from appended bytes (%d vs %d)", sink.Len(), len(appended))
	}

	var decoded foundationpb.ProjectionSnapshot
	if err := proto.Unmarshal(sink.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal streamed: %v", err)
	}
	if !proto.Equal(snapshot, &decoded) {
		t.Fatal("streamed snapshot is not proto-equal to the source")
	}
}

// TestWireFrameSubsetAssembly is the audience case: an arbitrary subset of a
// cached page assembles into a snapshot carrying exactly those records.
func TestWireFrameSubsetAssembly(t *testing.T) {
	mutations := wireTestMutations(50, 4)
	frames, total, err := frameMutations(mutations)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if total == 0 {
		t.Fatal("framed total is zero")
	}

	selected := make([][]byte, 0, len(mutations))
	expected := make([]*foundationpb.RecordMutation, 0, len(mutations))
	for i := range mutations {
		if i%7 != 0 {
			continue
		}
		selected = append(selected, frames[i])
		expected = append(expected, mutations[i])
	}

	scope := &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "signals", Collection: "ticks"}
	scopeBytes, err := proto.Marshal(scope)
	if err != nil {
		t.Fatalf("marshal scope: %v", err)
	}
	parts := snapshotWireParts{scopeBytes: scopeBytes, frames: selected, watermark: "9", epoch: 2}
	raw, err := parts.appendSnapshot(nil)
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	want := &foundationpb.ProjectionSnapshot{
		Scope:     scope,
		Batch:     &foundationpb.RecordMutationBatch{Mutations: expected},
		Watermark: "9",
		Epoch:     2,
	}
	var got foundationpb.ProjectionSnapshot
	if err := proto.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !proto.Equal(want, &got) {
		t.Fatalf("subset assembly mismatch: got %d records, want %d", len(got.GetBatch().GetMutations()), len(expected))
	}
}

// TestWireFramesAreReusableAcrossResponses confirms a cached frame is not
// consumed or mutated by assembly, so the same bytes can serve many subscribers.
func TestWireFramesAreReusableAcrossResponses(t *testing.T) {
	mutations := wireTestMutations(12, 4)
	frames, _, err := frameMutations(mutations)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	snapshotOf := func() []byte {
		parts := snapshotWireParts{frames: frames, watermark: "1", epoch: 1}
		raw, assembleErr := parts.appendSnapshot(nil)
		if assembleErr != nil {
			t.Fatalf("append: %v", assembleErr)
		}
		return raw
	}

	first := snapshotOf()
	for range 5 {
		if next := snapshotOf(); !bytes.Equal(first, next) {
			t.Fatal("repeated assembly from the same frames produced different bytes")
		}
	}

	// And the frames themselves are unchanged: reframing yields the same bytes.
	reframed, _, err := frameMutations(mutations)
	if err != nil {
		t.Fatalf("reframe: %v", err)
	}
	for i := range frames {
		if !bytes.Equal(frames[i], reframed[i]) {
			t.Fatalf("frame %d was mutated by assembly", i)
		}
	}
}

// errAfterNWriter fails partway through a response so the truncation contract is
// exercised rather than assumed.
type errAfterNWriter struct {
	limit int
	n     int
}

var errWireSinkClosed = errors.New("sink closed")

func (w *errAfterNWriter) Write(p []byte) (int, error) {
	if w.n >= w.limit {
		return 0, errWireSinkClosed
	}
	allowed := min(len(p), w.limit-w.n)
	w.n += allowed
	if allowed < len(p) {
		return allowed, errWireSinkClosed
	}
	return allowed, nil
}

func TestWireWriteSnapshotSurfacesWriterError(t *testing.T) {
	parts, err := snapshotPartsFromProto(&foundationpb.ProjectionSnapshot{
		Scope:     &foundationpb.ProjectionScope{TenantId: "org_1", Domain: "d", Collection: "c"},
		Batch:     &foundationpb.RecordMutationBatch{Mutations: wireTestMutations(40, 8)},
		Watermark: "5",
		Epoch:     2,
	})
	if err != nil {
		t.Fatalf("parts: %v", err)
	}

	sink := &errAfterNWriter{limit: 64}
	written, err := parts.writeSnapshot(sink)
	if !errors.Is(err, errWireSinkClosed) {
		t.Fatalf("error = %v, want errWireSinkClosed", err)
	}
	if written == 0 {
		t.Fatal("expected a partial byte count before the failure")
	}
	if written > int64(sink.limit) {
		t.Fatalf("reported %d bytes written past the sink limit of %d", written, sink.limit)
	}
}

func TestWireAppendSnapshotRejectsOversizedBody(t *testing.T) {
	if _, err := appendSnapshotHeader(nil, nil, -1); !errors.Is(err, ErrWireFrameTooLarge) {
		t.Fatalf("negative body error = %v, want ErrWireFrameTooLarge", err)
	}
}

// TestWireEmptyPageCarriesBatchField guards the one place proto3 presence rules
// bite: an empty page must still emit field 2, or a client cannot distinguish
// "no records" from a malformed response.
func TestWireEmptyPageCarriesBatchField(t *testing.T) {
	parts := snapshotWireParts{watermark: "3", epoch: 1}
	raw, err := parts.appendSnapshot(nil)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	var decoded foundationpb.ProjectionSnapshot
	if err := proto.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.GetBatch() == nil {
		t.Fatal("empty page lost the batch field; clients cannot tell it from a malformed response")
	}
	if len(decoded.GetBatch().GetMutations()) != 0 {
		t.Fatalf("empty page carries %d records", len(decoded.GetBatch().GetMutations()))
	}
}

var _ io.Writer = (*errAfterNWriter)(nil)
