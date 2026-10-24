package scratch

// Scope represents a nested execution checkpoint within an Arena.
// It supports stack-like sub-routine unwinds, preserving intermediate L1/L2 cache
// locality while preventing memory growth across sequential steps.
type Scope struct {
	arena      *Arena
	checkpoint int
}

// Scope captures the current allocation head of the arena.
// Calling End on the returned Scope rewinds the head to this checkpoint.
func (a *Arena) Scope() Scope {
	return Scope{
		arena:      a,
		checkpoint: a.head,
	}
}

// End restores the arena allocation head to the state recorded when Scope was created.
// This frees all allocations made within this scope in O(0) time.
func (s Scope) End() {
	if s.arena != nil && s.checkpoint >= s.arena.base && s.checkpoint <= s.arena.head {
		s.arena.head = s.checkpoint
	}
}

// BytesAllocated reports the number of bytes allocated since this Scope began.
func (s Scope) BytesAllocated() int {
	if s.arena == nil || s.arena.head < s.checkpoint {
		return 0
	}
	return s.arena.head - s.checkpoint
}
