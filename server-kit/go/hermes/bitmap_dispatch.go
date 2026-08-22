package hermes

import "sync/atomic"

// The bitmap dispatch seam: one place where an external executor can claim a
// word-kernel operation before the scalar interleaved loops run.
//
// Why a seam and not an import: the kernels here sit at the bottom of every
// columnar read, so this package cannot reach for runtime placement code
// without inverting a dependency boundary. Instead, an embedded host — which
// owns both the placement table and these kernels in one process — installs
// hooks after consulting placement itself. A hook that returns false hands
// the operation straight back to the scalar path.
//
// Contract for hook authors:
//
//   - Results must be bit-identical to the scalar kernel. These operations
//     are pure boolean algebra; any divergence corrupts projections silently.
//   - Install hooks only while the claimed lane is fresh and eligible per
//     the placement table; uninstall (ClearBitmapDispatchHooks) on staleness.
//     A stale claim costs more than the scalar loop it replaced.
//   - Hooks must not retain the operand slices beyond the call.
//
// Cost when unused: one atomic pointer load per kernel call against loops
// measured in hundreds of nanoseconds minimum. Existing benchmarks pin the
// scalar paths; a regression there fails those gates regardless of this seam.

// BitmapOp enumerates the in-place binary word kernels.
type BitmapOp uint8

const (
	// BitmapAnd computes dst[i] &= src[i].
	BitmapAnd BitmapOp = iota
	// BitmapOr computes dst[i] |= src[i].
	BitmapOr
	// BitmapAndNot computes dst[i] &^= src[i].
	BitmapAndNot
)

// BitmapCountHook claims a population count. Returns (count, handled).
type BitmapCountHook func(words []uint64) (int, bool)

// BitmapBinaryHook claims one in-place binary op. Returns handled.
//
// src is already resliced to len(dst) by the caller contract of the kernels,
// and both views share the caller's backing arrays.
type BitmapBinaryHook func(op BitmapOp, dst, src []uint64) bool

var (
	bitmapCountHook  atomic.Pointer[BitmapCountHook]
	bitmapBinaryHook atomic.Pointer[BitmapBinaryHook]
)

// SetBitmapCountHook installs a popcount takeover. nil clears it.
func SetBitmapCountHook(hook *BitmapCountHook) {
	bitmapCountHook.Store(hook)
}

// SetBitmapBinaryHook installs a binary-op takeover. nil clears it.
func SetBitmapBinaryHook(hook *BitmapBinaryHook) {
	bitmapBinaryHook.Store(hook)
}

// ClearBitmapDispatchHooks removes every installed hook. Tests and hosts call
// this on degradation so the scalar path is provably the only path again.
func ClearBitmapDispatchHooks() {
	SetBitmapCountHook(nil)
	SetBitmapBinaryHook(nil)
}

func tryCount(words []uint64) (int, bool) {
	if hook := bitmapCountHook.Load(); hook != nil {
		return (*hook)(words)
	}
	return 0, false
}

func tryBinary(op BitmapOp, dst, src []uint64) bool {
	if hook := bitmapBinaryHook.Load(); hook != nil {
		return (*hook)(op, dst, src)
	}
	return false
}
