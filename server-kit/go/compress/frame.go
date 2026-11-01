package compress

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
)

// The runtime frame is the compressed binary envelope of @ovasabi/runtime-transport.
// The layout must match runtime-transport/ts/src/compression.ts byte for byte:
//
//	offset 0  uint32 BE  magic 0x4f565254 ("OVRT")
//	offset 4  uint8      version (1)
//	offset 5  uint8      encoding id: 0 identity, 1 gzip, 2 br, 3 deflate
//	offset 6  uint16 BE  reserved (0)
//	offset 8  uint32 BE  raw (decoded) length
//	offset 12 uint32 BE  payload length
//	offset 16            payload
//
// "deflate" is the zlib format (RFC 1950), because the browser CompressionStream
// produces zlib for "deflate". It is not raw flate.
const (
	RuntimeFrameMagic       uint32 = 0x4f565254
	RuntimeFrameVersion     uint8  = 1
	RuntimeFrameHeaderBytes        = 16

	runtimeFrameIdentity uint8 = 0
	runtimeFrameGzip     uint8 = 1
	runtimeFrameBrotli   uint8 = 2
	runtimeFrameDeflate  uint8 = 3
)

// ErrRuntimeFrame reports a frame that is truncated, of another version, or inconsistent.
var ErrRuntimeFrame = errors.New("compress: invalid runtime frame")

// IsRuntimeFrame reports whether data starts with a runtime frame header.
// Unframed binary envelopes do not start with the magic, so both can share one socket.
func IsRuntimeFrame(data []byte) bool {
	return len(data) >= RuntimeFrameHeaderBytes && binary.BigEndian.Uint32(data) == RuntimeFrameMagic
}

// EncodeRuntimeFrameGzip compresses raw with the pooled gzip writer and frames it.
// It returns ok=false when the frame is not smaller than raw: the caller then sends raw.
func EncodeRuntimeFrameGzip(raw []byte, level int) (frame []byte, ok bool, err error) {
	if uint64(len(raw)) > uint64(^uint32(0)) {
		return nil, false, fmt.Errorf("%w: payload exceeds the uint32 length field", ErrRuntimeFrame)
	}
	compressed, err := CompressGzip(raw, level)
	if err != nil {
		return nil, false, err
	}
	if RuntimeFrameHeaderBytes+len(compressed) >= len(raw) {
		return nil, false, nil
	}
	frame = make([]byte, RuntimeFrameHeaderBytes+len(compressed))
	binary.BigEndian.PutUint32(frame[0:4], RuntimeFrameMagic)
	frame[4] = RuntimeFrameVersion
	frame[5] = runtimeFrameGzip
	// #nosec G115 -- both lengths are checked against MaxUint32 above.
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(raw)))
	binary.BigEndian.PutUint32(frame[12:16], uint32(len(compressed))) // #nosec G115 -- compressed is smaller than raw.
	copy(frame[RuntimeFrameHeaderBytes:], compressed)
	return frame, true, nil
}

// DecodeRuntimeFrame returns the decoded payload of a runtime frame.
// The decoded size is bounded by maxDecoded and must equal the declared raw length.
func DecodeRuntimeFrame(frame []byte, maxDecoded int64) ([]byte, error) {
	if !IsRuntimeFrame(frame) {
		return nil, fmt.Errorf("%w: missing header", ErrRuntimeFrame)
	}
	if frame[4] != RuntimeFrameVersion {
		return nil, fmt.Errorf("%w: version %d", ErrRuntimeFrame, frame[4])
	}
	rawLength := int64(binary.BigEndian.Uint32(frame[8:12]))
	payloadLength := int64(binary.BigEndian.Uint32(frame[12:16]))
	if payloadLength > int64(len(frame)-RuntimeFrameHeaderBytes) {
		return nil, fmt.Errorf("%w: truncated payload", ErrRuntimeFrame)
	}
	maxDecoded = normalizeDecodeLimit(maxDecoded)
	if rawLength > maxDecoded {
		return nil, ErrDecodedTooLarge
	}
	payload := frame[RuntimeFrameHeaderBytes : RuntimeFrameHeaderBytes+payloadLength]
	decoded, err := decodeRuntimeFramePayload(frame[5], payload, maxDecoded)
	if err != nil {
		return nil, err
	}
	if rawLength > 0 && int64(len(decoded)) != rawLength {
		return nil, fmt.Errorf("%w: decoded %d bytes, header says %d", ErrRuntimeFrame, len(decoded), rawLength)
	}
	return decoded, nil
}

func decodeRuntimeFramePayload(encoding uint8, payload []byte, maxDecoded int64) ([]byte, error) {
	switch encoding {
	case runtimeFrameIdentity:
		if int64(len(payload)) > maxDecoded {
			return nil, ErrDecodedTooLarge
		}
		return append([]byte(nil), payload...), nil
	case runtimeFrameGzip:
		zr, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		return readLimitedWithHint(zr, maxDecoded, len(payload)*4)
	case runtimeFrameDeflate:
		zr, err := zlib.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		return readLimitedWithHint(zr, maxDecoded, len(payload)*4)
	case runtimeFrameBrotli:
		return decompressBrotli(payload, maxDecoded)
	default:
		return nil, fmt.Errorf("%w: encoding id %d", ErrRuntimeFrame, encoding)
	}
}
