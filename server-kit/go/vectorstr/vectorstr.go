// Package vectorstr compares short strings against fixed 16-byte targets.
//
// Each compare copies at most 16 bytes of the input into a local array.
// It never reads memory past len(s), so checkptr and the race detector accept it.
package vectorstr

import "encoding/binary"

const (
	// MaxLen is the longest target that a 16-byte pad can hold.
	MaxLen = 16

	high8  = 0x8080808080808080
	geA    = 0x3f3f3f3f3f3f3f3f // byte + 0x3F sets bit 7 when byte >= 'A'.
	gtZ    = 0x2525252525252525 // byte + 0x25 sets bit 7 when byte > 'Z'.
	asciiN = high8
)

// MakePadded16 creates a 16-byte zero-padded array from a string up to 16 bytes.
func MakePadded16(s string) [16]byte {
	var buf [16]byte
	copy(buf[:], s)
	return buf
}

// load16 copies s into a zero-padded array and returns two little-endian words.
// The caller must make sure that len(s) <= 16.
func load16(s string) (uint64, uint64) {
	var buf [16]byte
	copy(buf[:], s)
	return binary.LittleEndian.Uint64(buf[0:8]), binary.LittleEndian.Uint64(buf[8:16])
}

// Equal16 compares string s against a 16-byte zero-padded target.
func Equal16(s string, targetPadded *[16]byte, targetLen int) bool {
	if len(s) != targetLen || targetLen > MaxLen {
		return false
	}
	w0, w1 := load16(s)
	return w0 == binary.LittleEndian.Uint64(targetPadded[0:8]) &&
		w1 == binary.LittleEndian.Uint64(targetPadded[8:16])
}

// foldWord converts ASCII 'A'..'Z' to lowercase in all 8 byte lanes.
// Other bytes do not change. All bytes must be below 0x80.
func foldWord(w uint64) uint64 {
	upper := (w + geA) &^ (w + gtZ) & high8
	return w | (upper >> 2)
}

// EqualFold16 compares s against a lowercase 16-byte zero-padded target.
// It folds ASCII letters only. Bytes at or above 0x80 must match exactly.
func EqualFold16(s string, lowerPadded *[16]byte, targetLen int) bool {
	if len(s) != targetLen || targetLen > MaxLen {
		return false
	}
	w0, w1 := load16(s)
	t0 := binary.LittleEndian.Uint64(lowerPadded[0:8])
	t1 := binary.LittleEndian.Uint64(lowerPadded[8:16])
	if (w0|w1)&asciiN != 0 {
		return equalFoldScalar(s, lowerPadded)
	}
	return foldWord(w0) == t0 && foldWord(w1) == t1
}

// equalFoldScalar is the per-byte path for input that holds non-ASCII bytes.
func equalFoldScalar(s string, lowerPadded *[16]byte) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 'A' && b <= 'Z' {
			b |= 0x20
		}
		if b != lowerPadded[i] {
			return false
		}
	}
	return true
}
