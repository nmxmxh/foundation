package compress

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runtimeFrameFixture is shared with runtime-transport/ts/src/compression.test.ts.
// The TS client must decode a frame this package produced, so the two layouts cannot drift.
const runtimeFrameFixture = "../../../runtime-transport/ts/src/__fixtures__/runtime_frame_gzip.hex"

func fixturePayload() []byte {
	return bytes.Repeat([]byte(`{"event_type":"projection:mutation:v1:success","record_id":"rec_000001"}`), 32)
}

func handFrame(encoding uint8, raw, payload []byte) []byte {
	frame := make([]byte, RuntimeFrameHeaderBytes+len(payload))
	binary.BigEndian.PutUint32(frame, RuntimeFrameMagic)
	frame[4] = RuntimeFrameVersion
	frame[5] = encoding
	binary.BigEndian.PutUint32(frame[8:], uint32(len(raw)))
	binary.BigEndian.PutUint32(frame[12:], uint32(len(payload)))
	copy(frame[RuntimeFrameHeaderBytes:], payload)
	return frame
}

func TestRuntimeFrameGzipRoundTrip(t *testing.T) {
	raw := fixturePayload()
	frame, ok, err := EncodeRuntimeFrameGzip(raw, gzip.BestSpeed)
	if err != nil || !ok {
		t.Fatalf("EncodeRuntimeFrameGzip = (ok %v, %v), want a frame", ok, err)
	}
	if !IsRuntimeFrame(frame) || frame[4] != RuntimeFrameVersion || frame[5] != runtimeFrameGzip {
		t.Fatalf("header = % x, want OVRT v1 gzip", frame[:8])
	}
	if got := binary.BigEndian.Uint32(frame[8:]); got != uint32(len(raw)) {
		t.Fatalf("raw length field = %d, want %d", got, len(raw))
	}
	if got := binary.BigEndian.Uint32(frame[12:]); int(got) != len(frame)-RuntimeFrameHeaderBytes {
		t.Fatalf("payload length field = %d, want %d", got, len(frame)-RuntimeFrameHeaderBytes)
	}
	decoded, err := DecodeRuntimeFrame(frame, 0)
	if err != nil || !bytes.Equal(decoded, raw) {
		t.Fatalf("DecodeRuntimeFrame round trip failed: %v", err)
	}
}

func TestRuntimeFrameSkipsPayloadsThatDoNotShrink(t *testing.T) {
	if frame, ok, err := EncodeRuntimeFrameGzip([]byte("tiny"), gzip.BestSpeed); err != nil || ok || frame != nil {
		t.Fatalf("tiny payload = (%v, %v, %v), want no frame", frame, ok, err)
	}
	if IsRuntimeFrame([]byte("OVRT")) {
		t.Fatal("a 4-byte prefix must not count as a frame")
	}
}

// The browser CompressionStream("deflate") emits zlib, so id 3 must decode zlib.
func TestRuntimeFrameDecodesOtherEncodings(t *testing.T) {
	raw := fixturePayload()
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	br, err := CompressBrotli(raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	for name, frame := range map[string][]byte{
		"deflate (zlib)": handFrame(runtimeFrameDeflate, raw, z.Bytes()),
		"brotli":         handFrame(runtimeFrameBrotli, raw, br),
		"identity":       handFrame(runtimeFrameIdentity, raw, raw),
	} {
		decoded, err := DecodeRuntimeFrame(frame, 0)
		if err != nil || !bytes.Equal(decoded, raw) {
			t.Fatalf("%s: decode failed: %v", name, err)
		}
	}
}

func TestRuntimeFrameRejectsBadFrames(t *testing.T) {
	raw := fixturePayload()
	good, _, err := EncodeRuntimeFrameGzip(raw, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(f func([]byte) []byte) []byte { return f(append([]byte(nil), good...)) }
	cases := map[string]struct {
		frame []byte
		limit int64
		want  error
	}{
		"not a frame":       {[]byte("plain binary envelope"), 0, ErrRuntimeFrame},
		"other version":     {mutate(func(b []byte) []byte { b[4] = 9; return b }), 0, ErrRuntimeFrame},
		"unknown encoding":  {mutate(func(b []byte) []byte { b[5] = 7; return b }), 0, ErrRuntimeFrame},
		"truncated payload": {good[:len(good)-1], 0, ErrRuntimeFrame},
		"length mismatch": {mutate(func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[8:], uint32(len(raw)-1))
			return b
		}), 0, ErrRuntimeFrame},
		"declared too large":   {good, int64(len(raw) - 1), ErrDecodedTooLarge},
		"identity too large":   {handFrame(runtimeFrameIdentity, nil, raw), int64(len(raw) - 1), ErrDecodedTooLarge},
		"gzip payload corrupt": {handFrame(runtimeFrameGzip, raw, []byte("not gzip")), 0, nil},
		"zlib payload corrupt": {handFrame(runtimeFrameDeflate, raw, []byte("not zlib")), 0, nil},
	}
	for name, tc := range cases {
		_, err := DecodeRuntimeFrame(tc.frame, tc.limit)
		if err == nil {
			t.Fatalf("%s: decode returned nil error", name)
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

// TestRuntimeFrameFixtureMatchesTypeScript keeps the shared fixture current.
// Regenerate with UPDATE_RUNTIME_FRAME_FIXTURE=1 go test ./compress -run FixtureMatches.
func TestRuntimeFrameFixtureMatchesTypeScript(t *testing.T) {
	path := filepath.FromSlash(runtimeFrameFixture)
	if os.Getenv("UPDATE_RUNTIME_FRAME_FIXTURE") == "1" {
		frame, ok, err := EncodeRuntimeFrameGzip(fixturePayload(), gzip.BestSpeed)
		if err != nil || !ok {
			t.Fatalf("encode fixture: ok=%v err=%v", ok, err)
		}
		if err := os.WriteFile(path, []byte(hex.EncodeToString(frame)+"\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("runtime-transport is not next to server-kit here (vendored copy); the foundation repo runs this")
	}
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	frame, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("fixture is not hex: %v", err)
	}
	decoded, err := DecodeRuntimeFrame(frame, 0)
	if err != nil || !bytes.Equal(decoded, fixturePayload()) {
		t.Fatalf("fixture does not decode to the shared payload: %v", err)
	}
}
