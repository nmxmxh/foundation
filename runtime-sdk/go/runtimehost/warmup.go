package runtimehost

import "os"

// DefaultWarmupUnitID is the unit id a worker sends at startup when no other is
// configured, to fault the child process in before a real caller arrives.
//
// Deliberately not a unit anyone registers. A kernel answers an unrecognised id
// with a non-zero status code in the control buffer rather than a transport
// error — the protocol has always required that, because a client can send any
// id at runtime — so the exchange completes, the child's code pages fault in,
// and nothing is dispatched. Registering a unit under this id would work too;
// there is just no reason to.
const DefaultWarmupUnitID = "runtime.warmup"

// warmMapping makes every page of a mapping resident before it is needed.
//
// A freshly created mapping has no pages backing it. Nothing is allocated until
// something touches an address, and then the kernel faults, finds or allocates
// a page, and installs it. Spread across an 8 MiB arena that is thousands of
// faults, and they all land on whichever exchange happens to be first: measured
// at ~374 us against a ~22 us warm round trip on darwin/arm64. The work is not
// avoidable, only movable, and moving it to startup is free because nothing is
// waiting on the pool yet.
//
// One byte per page is the whole job — a fault installs the page, not the byte.
// The write is a zero, so a caller may only pass a region whose contents are
// already zero or already expendable; the process pool calls this on a segment
// it has just created, before writing the arena header into it.
//
// Written rather than read deliberately. Faulting on a read installs a
// read-only entry for a shared file mapping, and the first write then takes a
// second fault to upgrade it — so warming by reading would leave half the cost
// in place for a region the host exists to write.
func warmMapping(raw []byte) {
	page := os.Getpagesize()
	if len(raw) == 0 || page <= 0 {
		return
	}
	for offset := 0; offset < len(raw); offset += page {
		raw[offset] = 0
	}
}
