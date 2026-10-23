package hermes

import (
	"encoding/binary"
	"math/bits"
)

const (
	swarZeroPattern = 0x0101010101010101
	swarHighPattern = 0x8080808080808080
)

// ScanDelimiter2 returns the index of the first occurrence of either b1 or b2 in s.
// It returns -1 if neither byte occurs in s.
func ScanDelimiter2(s []byte, b1, b2 byte) int {
	return scanDelimiter2Kernel(s, b1, b2)
}

// ScanDelimiter4 returns the index of the first occurrence of any of b1, b2, b3, b4 in s.
// It returns -1 if none of the bytes occur in s.
func ScanDelimiter4(s []byte, b1, b2, b3, b4 byte) int {
	return scanDelimiter4Kernel(s, b1, b2, b3, b4)
}

// ScanByte returns the index of the first occurrence of target in s.
// It returns -1 if target is not present in s.
func ScanByte(s []byte, target byte) int {
	return scanByteKernel(s, target)
}

// scanDelimiter2Scalar is the reference byte-by-byte implementation.
func scanDelimiter2Scalar(s []byte, b1, b2 byte) int {
	for i, c := range s {
		if c == b1 || c == b2 {
			return i
		}
	}
	return -1
}

// scanDelimiter4Scalar is the reference byte-by-byte implementation.
func scanDelimiter4Scalar(s []byte, b1, b2, b3, b4 byte) int {
	for i, c := range s {
		if c == b1 || c == b2 || c == b3 || c == b4 {
			return i
		}
	}
	return -1
}

// scanByteScalar is the reference byte-by-byte implementation.
func scanByteScalar(s []byte, target byte) int {
	for i, c := range s {
		if c == target {
			return i
		}
	}
	return -1
}

// scanDelimiter2SWAR uses 64-bit word-level parallel operations.
// It scans 8 bytes per iteration without conditional branches in the hot loop.
func scanDelimiter2SWAR(s []byte, b1, b2 byte) int {
	n := len(s)
	if n < 8 {
		return scanDelimiter2Scalar(s, b1, b2)
	}

	mask1 := uint64(b1) * swarZeroPattern
	mask2 := uint64(b2) * swarZeroPattern

	i := 0
	limit := n - 8
	for ; i <= limit; i += 8 {
		word := binary.LittleEndian.Uint64(s[i : i+8])
		v1 := word ^ mask1
		v2 := word ^ mask2
		has1 := (v1 - swarZeroPattern) & ^v1 & swarHighPattern
		has2 := (v2 - swarZeroPattern) & ^v2 & swarHighPattern
		match := has1 | has2
		if match != 0 {
			return i + (bits.TrailingZeros64(match) >> 3)
		}
	}

	for ; i < n; i++ {
		c := s[i]
		if c == b1 || c == b2 {
			return i
		}
	}
	return -1
}

// scanDelimiter4SWAR uses 64-bit word-level parallel operations for four targets.
func scanDelimiter4SWAR(s []byte, b1, b2, b3, b4 byte) int {
	n := len(s)
	if n < 8 {
		return scanDelimiter4Scalar(s, b1, b2, b3, b4)
	}

	mask1 := uint64(b1) * swarZeroPattern
	mask2 := uint64(b2) * swarZeroPattern
	mask3 := uint64(b3) * swarZeroPattern
	mask4 := uint64(b4) * swarZeroPattern

	i := 0
	limit := n - 8
	for ; i <= limit; i += 8 {
		word := binary.LittleEndian.Uint64(s[i : i+8])
		v1 := word ^ mask1
		v2 := word ^ mask2
		v3 := word ^ mask3
		v4 := word ^ mask4
		has1 := (v1 - swarZeroPattern) & ^v1 & swarHighPattern
		has2 := (v2 - swarZeroPattern) & ^v2 & swarHighPattern
		has3 := (v3 - swarZeroPattern) & ^v3 & swarHighPattern
		has4 := (v4 - swarZeroPattern) & ^v4 & swarHighPattern
		match := has1 | has2 | has3 | has4
		if match != 0 {
			return i + (bits.TrailingZeros64(match) >> 3)
		}
	}

	for ; i < n; i++ {
		c := s[i]
		if c == b1 || c == b2 || c == b3 || c == b4 {
			return i
		}
	}
	return -1
}

// scanByteSWAR uses 64-bit word-level parallel operations for a single target byte.
func scanByteSWAR(s []byte, target byte) int {
	n := len(s)
	if n < 8 {
		return scanByteScalar(s, target)
	}

	mask := uint64(target) * swarZeroPattern
	i := 0
	limit := n - 8
	for ; i <= limit; i += 8 {
		word := binary.LittleEndian.Uint64(s[i : i+8])
		v := word ^ mask
		match := (v - swarZeroPattern) & ^v & swarHighPattern
		if match != 0 {
			return i + (bits.TrailingZeros64(match) >> 3)
		}
	}

	for ; i < n; i++ {
		if s[i] == target {
			return i
		}
	}
	return -1
}
