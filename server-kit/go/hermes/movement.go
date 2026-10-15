package hermes

// Count explicit payload accesses separately from runtime and hardware traffic.
type movementCounter uint8

const (
	movementNumericRead movementCounter = iota
	movementNumericWrite
	movementBitmapRead
	movementBitmapWrite
	movementCopy
	movementParseInput
	movementCandidates
	movementFieldProbes
	movementSortComparisons
	movementRowsMaterialized
	movementUnobservedKernels
	movementCounterCount
)

// Count the elements that copy actually transfers, including short destinations.
func copyBitmapWords(dst, src []uint64) int {
	n := copy(dst, src)
	movementRecord(movementCopy, n*8)
	movementRecord(movementBitmapRead, n*8)
	movementRecord(movementBitmapWrite, n*8)
	return n
}
