package hermes

import "math/bits"

// This file centralizes the 4-way interleaved ("Memory-Level Parallelism")
// loop kernels used across the columnar engine's bitmap and reduction paths.
//
// A naive reduction or element-wise word loop carries a dependency through a
// single accumulator or a single destination stream, so each iteration stalls
// on the previous iteration's latency. Splitting the work into four independent
// lanes lets the CPU keep multiple operations in flight per cycle; measured up
// to ~4x throughput on scalar reductions with no SIMD build dependency
// (source: Lemire 2026). See docs/optimization_points.md (item 60) and the
// columnar section of docs/foundation_benchmarks.md.
//
// All in-place word helpers require len(src) >= len(dst): the src reslice makes
// a shorter src a loud panic (never silent corruption) and gives the compiler
// the bounds-check-elimination hint for the hot loop. Callers hold this via
// SelectionBitmap.sameShape or by slicing both operands to a common length.

// popcountWords returns the total set-bit count across words. bits.OnesCount64
// is recognized by the Go compiler and lowers to a single POPCNT (x86) / CNT
// (ARM) instruction per word.
func popcountWords(words []uint64) int {
	if count, handled := tryCount(words); handled {
		return count
	}
	var c0, c1, c2, c3 int
	i := 0
	for ; i+4 <= len(words); i += 4 {
		c0 += bits.OnesCount64(words[i])
		c1 += bits.OnesCount64(words[i+1])
		c2 += bits.OnesCount64(words[i+2])
		c3 += bits.OnesCount64(words[i+3])
	}
	total := (c0 + c1) + (c2 + c3)
	for ; i < len(words); i++ {
		total += bits.OnesCount64(words[i])
	}
	return total
}

// andWords computes dst[i] &= src[i] in place for every word of dst.
func andWords(dst, src []uint64) {
	src = src[:len(dst)] // panics loudly if src is shorter; also a BCE hint
	if tryBinary(BitmapAnd, dst, src) {
		return
	}
	i := 0
	for ; i+4 <= len(dst); i += 4 {
		dst[i] &= src[i]
		dst[i+1] &= src[i+1]
		dst[i+2] &= src[i+2]
		dst[i+3] &= src[i+3]
	}
	for ; i < len(dst); i++ {
		dst[i] &= src[i]
	}
}

// orWords computes dst[i] |= src[i] in place for every word of dst.
func orWords(dst, src []uint64) {
	src = src[:len(dst)]
	if tryBinary(BitmapOr, dst, src) {
		return
	}
	i := 0
	for ; i+4 <= len(dst); i += 4 {
		dst[i] |= src[i]
		dst[i+1] |= src[i+1]
		dst[i+2] |= src[i+2]
		dst[i+3] |= src[i+3]
	}
	for ; i < len(dst); i++ {
		dst[i] |= src[i]
	}
}

// andNotWords computes dst[i] &^= src[i] in place for every word of dst.
func andNotWords(dst, src []uint64) {
	src = src[:len(dst)]
	if tryBinary(BitmapAndNot, dst, src) {
		return
	}
	i := 0
	for ; i+4 <= len(dst); i += 4 {
		dst[i] &^= src[i]
		dst[i+1] &^= src[i+1]
		dst[i+2] &^= src[i+2]
		dst[i+3] &^= src[i+3]
	}
	for ; i < len(dst); i++ {
		dst[i] &^= src[i]
	}
}

// notWords complements every word of dst in place.
func notWords(dst []uint64) {
	i := 0
	for ; i+4 <= len(dst); i += 4 {
		dst[i] = ^dst[i]
		dst[i+1] = ^dst[i+1]
		dst[i+2] = ^dst[i+2]
		dst[i+3] = ^dst[i+3]
	}
	for ; i < len(dst); i++ {
		dst[i] = ^dst[i]
	}
}
