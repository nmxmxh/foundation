//! Frame-scoped linear bump allocator, mirroring server-kit/go/scratch.
//! Provides O(1) aligned bump allocations and O(1) reset with zero heap churn.

pub const DEFAULT_SCRATCH_CAPACITY: usize = 64 * 1024; // 64 KiB

#[derive(Debug, PartialEq, Eq)]
pub enum ScratchError {
    CapacityExceeded { requested: usize, remaining: usize },
    ZeroAllocation,
}

impl std::fmt::Display for ScratchError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ScratchError::CapacityExceeded { requested, remaining } => {
                write!(
                    f,
                    "scratch arena capacity exceeded: requested {requested} bytes, {remaining} bytes remaining"
                )
            }
            ScratchError::ZeroAllocation => write!(f, "scratch arena requested zero bytes"),
        }
    }
}

impl std::error::Error for ScratchError {}

/// ScratchArena provides reusable, single-worker linear bump memory.
/// All allocations are aligned to 8-byte boundaries.
pub struct ScratchArena {
    buffer: Vec<u8>,
    head: usize,
}

impl Default for ScratchArena {
    fn default() -> Self {
        Self::with_capacity(DEFAULT_SCRATCH_CAPACITY)
    }
}

impl ScratchArena {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn with_capacity(capacity: usize) -> Self {
        let cap = capacity.max(8);
        Self { buffer: vec![0u8; cap], head: 0 }
    }

    #[inline]
    pub fn capacity(&self) -> usize {
        self.buffer.len()
    }

    #[inline]
    pub fn allocated(&self) -> usize {
        self.head
    }

    #[inline]
    pub fn remaining(&self) -> usize {
        self.buffer.len().saturating_sub(self.head)
    }

    /// reset clears the bump pointer in O(1) without releasing buffer memory.
    #[inline]
    pub fn reset(&mut self) {
        self.head = 0;
    }

    /// alloc_bytes reserves an 8-byte aligned subslice of the requested length.
    pub fn alloc_bytes(&mut self, length: usize) -> Result<&mut [u8], ScratchError> {
        if length == 0 {
            return Err(ScratchError::ZeroAllocation);
        }
        let aligned_head = (self.head + 7) & !7;
        let next_head = aligned_head.checked_add(length).ok_or(ScratchError::CapacityExceeded {
            requested: length,
            remaining: self.remaining(),
        })?;

        if next_head > self.buffer.len() {
            return Err(ScratchError::CapacityExceeded {
                requested: length,
                remaining: self.buffer.len().saturating_sub(aligned_head),
            });
        }

        self.head = next_head;
        Ok(&mut self.buffer[aligned_head..next_head])
    }

    /// alloc_copy copies data into the arena and returns an immutable borrowed view.
    pub fn alloc_copy<'a>(&'a mut self, data: &[u8]) -> Result<&'a [u8], ScratchError> {
        let slice = self.alloc_bytes(data.len())?;
        slice.copy_from_slice(data);
        Ok(&*slice)
    }

    /// Returns the remaining unallocated capacity as a mutable slice.
    /// Aligns the allocation head to an 8-byte boundary first.
    pub fn remaining_slice_mut(&mut self) -> &mut [u8] {
        let aligned_head = (self.head + 7) & !7;
        if aligned_head >= self.buffer.len() {
            &mut []
        } else {
            self.head = aligned_head;
            &mut self.buffer[aligned_head..]
        }
    }

    /// Commits written bytes starting from the aligned head.
    pub fn commit(&mut self, bytes: usize) -> Result<&mut [u8], ScratchError> {
        if bytes == 0 {
            return Err(ScratchError::ZeroAllocation);
        }
        let next_head = self.head.checked_add(bytes).ok_or(ScratchError::CapacityExceeded {
            requested: bytes,
            remaining: self.remaining(),
        })?;

        if next_head > self.buffer.len() {
            return Err(ScratchError::CapacityExceeded {
                requested: bytes,
                remaining: self.buffer.len().saturating_sub(self.head),
            });
        }

        let start = self.head;
        self.head = next_head;
        Ok(&mut self.buffer[start..next_head])
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_scratch_arena_alloc_and_reset() {
        let mut arena = ScratchArena::with_capacity(1024);
        assert_eq!(arena.capacity(), 1024);
        assert_eq!(arena.allocated(), 0);

        let slice1 = arena.alloc_bytes(100).expect("alloc 100");
        assert_eq!(slice1.len(), 100);
        slice1.fill(42);

        let slice2 = arena.alloc_bytes(50).expect("alloc 50");
        assert_eq!(slice2.len(), 50);

        // Aligned head: 100 aligned to 8 is 104; 104 + 50 = 154
        assert_eq!(arena.allocated(), 154);

        arena.reset();
        assert_eq!(arena.allocated(), 0);
        assert_eq!(arena.remaining(), 1024);
    }

    #[test]
    fn test_scratch_arena_capacity_exhaustion() {
        let mut arena = ScratchArena::with_capacity(64);
        let _ = arena.alloc_bytes(60).expect("alloc 60");
        let err = arena.alloc_bytes(10).expect_err("must exceed capacity");
        match err {
            ScratchError::CapacityExceeded { requested, remaining } => {
                assert_eq!(requested, 10);
                assert!(remaining < 10);
            }
            _ => panic!("unexpected error variant"),
        }
    }

    #[test]
    fn test_scratch_arena_alloc_copy() {
        let mut arena = ScratchArena::with_capacity(256);
        let payload = b"foundation runtime scratch";
        let copied = arena.alloc_copy(payload).expect("copy");
        assert_eq!(copied, payload);
    }

    #[test]
    fn test_scratch_arena_commit() {
        let mut arena = ScratchArena::with_capacity(256);
        let rem = arena.remaining_slice_mut();
        assert!(rem.len() >= 256);
        rem[..4].copy_from_slice(b"test");
        let committed = arena.commit(4).expect("commit 4 bytes");
        assert_eq!(committed, b"test");
        assert_eq!(arena.allocated(), 4);

        assert_eq!(arena.commit(0), Err(ScratchError::ZeroAllocation));
    }
}
