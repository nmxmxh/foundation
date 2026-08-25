package grpcsvc

import (
	"testing"
)

// TE-20 regression for a defect repaired on 2026-08-25.
//
// binaryFrameCodec.Marshal accepted a *Frame in intent only. The inner type
// assertion declared its own ok, shadowing the outer one, so the `ok = true`
// that followed set a variable that went out of scope immediately and the
// caller was told "expects grpcsvc.Frame, got *grpcsvc.Frame". gRPC hands a
// codec a pointer to the message, so this was the shape that mattered most,
// and it was the one shape no test exercised — every existing test and
// benchmark passes a Frame by value, where the outer assertion succeeds and the
// broken branch never runs. ineffassign had been reporting it as an ineffectual
// assignment the whole time.
func TestBinaryCodecMarshalsFrameByPointer(t *testing.T) {
	codec := binaryFrameCodec{}
	frame := Frame{
		EventType:     "media:process_asset:v1:requested",
		Payload:       []byte("payload"),
		CorrelationID: "corr-1",
		SchemaVersion: "1.0",
	}

	byValue, err := codec.Marshal(frame)
	if err != nil {
		t.Fatalf("Marshal(Frame) error = %v", err)
	}

	byPointer, err := codec.Marshal(&frame)
	if err != nil {
		t.Fatalf("Marshal(*Frame) error = %v; the pointer form is what gRPC passes", err)
	}

	if string(byValue) != string(byPointer) {
		t.Fatalf("value and pointer marshals differ:\n value=%q\n   ptr=%q", byValue, byPointer)
	}

	// And the bytes must still round-trip.
	var out Frame
	if err := codec.Unmarshal(byPointer, &out); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if out.EventType != frame.EventType || string(out.Payload) != string(frame.Payload) ||
		out.CorrelationID != frame.CorrelationID || out.SchemaVersion != frame.SchemaVersion {
		t.Fatalf("round trip mismatch: got %+v want %+v", out, frame)
	}
}

// TestBinaryCodecRejectsUnsupportedTypes keeps the refusal path honest: fixing
// the pointer case must not make the codec accept anything at all.
func TestBinaryCodecRejectsUnsupportedTypes(t *testing.T) {
	codec := binaryFrameCodec{}
	for _, v := range []any{nil, "frame", 42, (*Frame)(nil), struct{}{}} {
		if _, err := codec.Marshal(v); err == nil {
			t.Fatalf("Marshal(%T) accepted an unsupported value", v)
		}
	}
}
