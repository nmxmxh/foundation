//go:build amd64 && goexperiment.simd

package hermes

import (
	"math/bits"
	"simd/archsimd"
)

// scanDelimiter2Kernel uses 32-byte AVX2 vector operations on amd64 builds.
func scanDelimiter2Kernel(s []byte, b1, b2 byte) int {
	n := len(s)
	if !archsimd.X86.AVX2() || n < 32 {
		return scanDelimiter2SWAR(s, b1, b2)
	}

	t1 := archsimd.BroadcastUint8x32(b1)
	t2 := archsimd.BroadcastUint8x32(b2)

	i := 0
	limit := n - 32
	for ; i <= limit; i += 32 {
		chunk := archsimd.LoadUint8x32Slice(s[i : i+32])
		m1 := chunk.Equal(t1)
		m2 := chunk.Equal(t2)
		m := m1.Or(m2)
		maskBits := m.ToBits()
		if maskBits != 0 {
			return i + bits.TrailingZeros32(maskBits)
		}
	}

	if i < n {
		tailIdx := scanDelimiter2SWAR(s[i:], b1, b2)
		if tailIdx >= 0 {
			return i + tailIdx
		}
	}
	return -1
}

// scanDelimiter4Kernel uses 32-byte AVX2 vector operations for four target bytes.
func scanDelimiter4Kernel(s []byte, b1, b2, b3, b4 byte) int {
	n := len(s)
	if !archsimd.X86.AVX2() || n < 32 {
		return scanDelimiter4SWAR(s, b1, b2, b3, b4)
	}

	t1 := archsimd.BroadcastUint8x32(b1)
	t2 := archsimd.BroadcastUint8x32(b2)
	t3 := archsimd.BroadcastUint8x32(b3)
	t4 := archsimd.BroadcastUint8x32(b4)

	i := 0
	limit := n - 32
	for ; i <= limit; i += 32 {
		chunk := archsimd.LoadUint8x32Slice(s[i : i+32])
		m1 := chunk.Equal(t1)
		m2 := chunk.Equal(t2)
		m3 := chunk.Equal(t3)
		m4 := chunk.Equal(t4)
		m := m1.Or(m2).Or(m3.Or(m4))
		maskBits := m.ToBits()
		if maskBits != 0 {
			return i + bits.TrailingZeros32(maskBits)
		}
	}

	if i < n {
		tailIdx := scanDelimiter4SWAR(s[i:], b1, b2, b3, b4)
		if tailIdx >= 0 {
			return i + tailIdx
		}
	}
	return -1
}

// scanByteKernel uses 32-byte AVX2 vector operations for single-byte scanning.
func scanByteKernel(s []byte, target byte) int {
	n := len(s)
	if !archsimd.X86.AVX2() || n < 32 {
		return scanByteSWAR(s, target)
	}

	t := archsimd.BroadcastUint8x32(target)

	i := 0
	limit := n - 32
	for ; i <= limit; i += 32 {
		chunk := archsimd.LoadUint8x32Slice(s[i : i+32])
		m := chunk.Equal(t)
		maskBits := m.ToBits()
		if maskBits != 0 {
			return i + bits.TrailingZeros32(maskBits)
		}
	}

	if i < n {
		tailIdx := scanByteSWAR(s[i:], target)
		if tailIdx >= 0 {
			return i + tailIdx
		}
	}
	return -1
}
