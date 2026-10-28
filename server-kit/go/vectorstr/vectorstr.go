package vectorstr

import (
	"encoding/binary"
	"unsafe"
)

// PageSizeMask identifies the offset within a 4096-byte virtual memory page.
const PageSizeMask = 0xFFF

// PageBoundaryRoom ensures that reading 16 bytes will not cross a page boundary.
const PageBoundaryRoom = 4096 - 16

// lenMasks provides bitmasks for lengths 0 through 8.
var lenMasks = [9]uint64{
	0x0000000000000000,
	0x00000000000000FF,
	0x000000000000FFFF,
	0x0000000000FFFFFF,
	0x00000000FFFFFFFF,
	0x000000FFFFFFFFFF,
	0x0000FFFFFFFFFFFF,
	0x00FFFFFFFFFFFFFF,
	0xFFFFFFFFFFFFFFFF,
}

// MakePadded16 creates a 16-byte zero-padded array from a string up to 16 bytes.
func MakePadded16(s string) [16]byte {
	var buf [16]byte
	copy(buf[:], s)
	return buf
}

// Equal16 compares string s against a 16-byte zero-padded target with constant-time
// 64-bit register operations when memory is page-safe.
func Equal16(s string, targetPadded *[16]byte, targetLen int) bool {
	if len(s) != targetLen {
		return false
	}
	if targetLen == 0 {
		return true
	}
	if targetLen > 16 {
		return s == string(targetPadded[:16])
	}

	ptr := unsafe.StringData(s) // #nosec G103 -- string buffer base pointer.
	// Guard against virtual memory page boundary crossing to prevent SIGSEGV.
	addr := uintptr(unsafe.Pointer(ptr)) // #nosec G103 -- page boundary calculation.
	if (addr & PageSizeMask) > PageBoundaryRoom {
		return s == string(targetPadded[:targetLen])
	}

	tgt0 := binary.LittleEndian.Uint64(targetPadded[0:8])

	if targetLen <= 8 {
		mask := lenMasks[targetLen]
		src0 := *(*uint64)(unsafe.Pointer(ptr)) // #nosec G103 -- page-safe 64-bit word read.
		return (src0 & mask) == tgt0
	}

	// targetLen > 8 and <= 16
	src0 := *(*uint64)(unsafe.Pointer(ptr)) // #nosec G103 -- page-safe 64-bit word read.
	if src0 != tgt0 {
		return false
	}

	mask1 := lenMasks[targetLen-8]
	src1 := *(*uint64)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + 8)) // #nosec G103 -- page-safe second 64-bit word read.
	tgt1 := binary.LittleEndian.Uint64(targetPadded[8:16])
	return (src1 & mask1) == tgt1
}

// EqualFold16 provides case-insensitive 16-byte ASCII comparison.
func EqualFold16(s string, lowerPadded *[16]byte, targetLen int) bool {
	if len(s) != targetLen {
		return false
	}
	if targetLen == 0 {
		return true
	}
	if targetLen > 16 {
		return false
	}

	ptr := unsafe.StringData(s) // #nosec G103 -- string buffer base pointer.
	addr := uintptr(unsafe.Pointer(ptr)) // #nosec G103 -- page boundary calculation.
	if (addr & PageSizeMask) > PageBoundaryRoom {
		// Fallback for page boundaries
		for i := range targetLen {
			c := s[i]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != lowerPadded[i] {
				return false
			}
		}
		return true
	}

	// ASCII uppercase to lowercase bitmask (bit 5: 0x20)
	const toLowerMask = 0x2020202020202020

	tgt0 := binary.LittleEndian.Uint64(lowerPadded[0:8])
	if targetLen <= 8 {
		mask := lenMasks[targetLen]
		src0 := *(*uint64)(unsafe.Pointer(ptr)) // #nosec G103 -- page-safe 64-bit word read.
		// Convert ASCII uppercase to lowercase in parallel across 8 bytes
		folded0 := src0 | toLowerMask
		return (folded0 & mask) == (tgt0 & mask)
	}

	src0 := *(*uint64)(unsafe.Pointer(ptr)) // #nosec G103 -- page-safe 64-bit word read.
	folded0 := src0 | toLowerMask
	if (folded0 & lenMasks[8]) != (tgt0 & lenMasks[8]) {
		return false
	}

	mask1 := lenMasks[targetLen-8]
	src1 := *(*uint64)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + 8)) // #nosec G103 -- page-safe second 64-bit word read.
	tgt1 := binary.LittleEndian.Uint64(lowerPadded[8:16])
	folded1 := src1 | toLowerMask
	return (folded1 & mask1) == (tgt1 & mask1)
}
