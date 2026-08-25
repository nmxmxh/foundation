package runtimehost

import "errors"

// ErrDispatchLaneContended reports that a bounded retry on a dispatch stat row
// ran out of attempts while the row was still contended.
//
// It is deliberately distinct from the ordinary refusals. A refusal means the
// row was in a state that made the operation meaningless — releasing a lane
// already at zero. This means the operation was legitimate and did not land, so
// the caller still owns the bookkeeping and must retry or surface it.
//
// Under the single-writer discipline for a stat row this never fires: one owner
// per lane means the compare-and-swap has nobody to lose to. Seeing it is
// evidence that the discipline has been broken somewhere upstream.
var ErrDispatchLaneContended = errors.New("dispatch lane contended beyond its retry budget")
