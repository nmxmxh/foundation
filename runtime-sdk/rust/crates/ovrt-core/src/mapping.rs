//! A file-backed `MAP_SHARED` mapping, and the reason it exists.
//!
//! Two processes agree on a region either through mappings or through
//! positional file I/O. Mixing the two is what this module removes. The Go host
//! maps its control buffer with `syscall.Mmap`; before this type existed the
//! Rust kernel opened the same file and used `pread`/`pwrite`, so a "shared
//! memory" exchange copied the whole buffer out of the kernel and back in on
//! every call — two syscalls and a heap allocation for a region that was
//! already addressable. Mapping both ends makes the region genuinely shared:
//! a store on one side is visible to the other with no syscall at all.
//!
//! Why this lives in `ovrt-core` rather than where it is used: `ovrt-native` is
//! `#![forbid(unsafe_code)]` and that property is enforced, so it cannot call
//! `mmap`. This crate already owns the raw-pointer control-plane code (see
//! [`crate::log_ring`]) and is the right home for one more piece of it. The
//! unsafe is contained here and the callers stay safe.
//!
//! `libc` is used directly rather than through a mapping crate: it is a
//! declaration crate — `extern "C"` signatures and constants, no runtime layer —
//! so the call compiles to the syscall with nothing in between.

#[cfg(unix)]
mod unix {
    use std::fs::OpenOptions;
    use std::os::unix::io::AsRawFd;
    use std::path::Path;
    use std::sync::atomic::AtomicU32;

    /// A writable `MAP_SHARED` view of a file, unmapped on drop.
    ///
    /// The mapping outlives the descriptor that created it — POSIX keeps a
    /// mapping valid after `close(2)` — so no file handle is retained.
    pub struct SharedMapping {
        ptr: *mut u8,
        len: usize,
    }

    // SAFETY: The handle owns its mapping and holds no thread-affine state. The
    // pointer refers to a MAP_SHARED region that stays valid until `Drop` runs,
    // so moving the handle between threads does not invalidate it. Concurrent
    // access is the caller's protocol to arrange (see `as_mut_slice`), exactly
    // as it is for the peer process, which no Rust type system can police.
    unsafe impl Send for SharedMapping {}
    // SAFETY: Every `&self` method either reads bytes or hands out an
    // `&AtomicU32`, both of which are sound to share. Mutation requires
    // `&mut self`, so `&SharedMapping` cannot produce a non-atomic write.
    unsafe impl Sync for SharedMapping {}

    impl SharedMapping {
        /// Opens `path` and maps its first `len` bytes read/write.
        ///
        /// Fails rather than truncating when the file is shorter than `len`:
        /// mapping past the end of a file yields `SIGBUS` on access, which
        /// surfaces as a crash with no diagnostic instead of an error at the
        /// point the mismatch actually occurred.
        pub fn open(path: &Path, len: usize) -> Result<Self, String> {
            if len == 0 {
                return Err("shared mapping length must be positive".to_string());
            }
            let file = OpenOptions::new()
                .read(true)
                .write(true)
                .open(path)
                .map_err(|error| format!("open shared mapping {}: {error}", path.display()))?;
            let file_len = file
                .metadata()
                .map_err(|error| format!("stat shared mapping {}: {error}", path.display()))?
                .len();
            if file_len < len as u64 {
                return Err(format!(
                    "shared mapping {} is {file_len} bytes; {len} were requested",
                    path.display()
                ));
            }

            // SAFETY: `fd` is open for read and write and refers to a regular
            // file of at least `len` bytes, checked above. A null address lets
            // the kernel choose the placement, and the length is non-zero. The
            // call either returns a mapping of exactly `len` writable bytes or
            // MAP_FAILED, which is handled below.
            let addr = unsafe {
                libc::mmap(
                    std::ptr::null_mut(),
                    len,
                    libc::PROT_READ | libc::PROT_WRITE,
                    libc::MAP_SHARED,
                    file.as_raw_fd(),
                    0,
                )
            };
            if addr == libc::MAP_FAILED {
                return Err(format!(
                    "map shared region {} of {len} bytes: {}",
                    path.display(),
                    std::io::Error::last_os_error()
                ));
            }
            Ok(Self { ptr: addr.cast::<u8>(), len })
        }

        pub fn len(&self) -> usize {
            self.len
        }

        pub fn is_empty(&self) -> bool {
            self.len == 0
        }

        /// Borrows the mapping immutably.
        ///
        /// A peer process writing the region concurrently is a data race that
        /// this signature cannot express. Callers must observe the epoch that
        /// publishes a region before reading it; see the runtime buffer's
        /// `IDX_*` slots for which store makes which region readable.
        pub fn as_slice(&self) -> &[u8] {
            // SAFETY: `open` returns only on a successful mapping of exactly
            // `len` readable bytes, and `Drop` is the sole unmapper, so the
            // region is live for the borrow.
            unsafe { std::slice::from_raw_parts(self.ptr, self.len) }
        }

        /// Borrows the mapping mutably.
        ///
        /// The exclusivity `&mut` promises holds within this process only. The
        /// peer must be known not to be writing — under the current transport
        /// the host blocks on the acknowledgement while the kernel works, so
        /// the two never hold the region at once.
        pub fn as_mut_slice(&mut self) -> &mut [u8] {
            // SAFETY: As `as_slice`, and `&mut self` rules out another borrow
            // from this process for the duration.
            unsafe { std::slice::from_raw_parts_mut(self.ptr, self.len) }
        }

        /// Copies `src` into the mapping at `offset`, through a shared handle.
        ///
        /// `&self` rather than `&mut self` because the region is partitioned by
        /// protocol, not by borrow: the producer allocates disjoint slabs and
        /// each consumer writes only into the one it was given, so two writers
        /// never touch the same bytes and no exclusive handle exists to hand
        /// out. This is the same contract the positional-write API it replaced
        /// carried; the difference is that the bytes land in the page the peer
        /// is already reading instead of being copied there.
        ///
        /// The caller owns that partitioning. Bounds against the mapping are
        /// checked here; bounds against a *slab* are the caller's, because only
        /// the caller knows which slab it was assigned.
        pub fn write_at(&self, offset: usize, src: &[u8]) -> Result<(), String> {
            if src.is_empty() {
                return Ok(());
            }
            let end = offset
                .checked_add(src.len())
                .ok_or_else(|| format!("write offset overflow at {offset}"))?;
            if end > self.len {
                return Err(format!(
                    "write of {} bytes at {offset} runs past the {}-byte mapping",
                    src.len(),
                    self.len
                ));
            }
            // SAFETY: Bounds are checked above, so the destination is `src.len()`
            // bytes inside a live mapping. Source and destination cannot overlap:
            // `src` is a caller-owned slice and, per this method's contract, no
            // other writer holds the destination range.
            unsafe {
                std::ptr::copy_nonoverlapping(src.as_ptr(), self.ptr.add(offset), src.len());
            }
            Ok(())
        }

        /// Borrows a 4-byte word of the mapping as an atomic.
        ///
        /// This is how a cross-process epoch is meant to be read and written:
        /// the word is shared, so ordinary loads and stores are races, and only
        /// atomics with explicit ordering make a published region visible. The
        /// offset must be 4-byte aligned; mappings begin on a page boundary, so
        /// alignment of the base is guaranteed by the kernel.
        pub fn atomic_u32(&self, offset: usize) -> Result<&AtomicU32, String> {
            let end = offset
                .checked_add(4)
                .ok_or_else(|| format!("atomic offset overflow at {offset}"))?;
            if end > self.len {
                return Err(format!(
                    "atomic word at {offset} runs past the {}-byte mapping",
                    self.len
                ));
            }
            // `% 4` rather than `offset.is_multiple_of(4)`.
            //
            // is_multiple_of stabilised in Rust 1.87. This crate declares no
            // rust-version and is vendored into eleven applications that each
            // pin their own toolchain; one builds on rust:1.85-alpine, where
            // that method is E0658 and the entire workspace fails to compile.
            // The MSRV was raised silently, at deploy time, by a convenience
            // method that reads no better than the remainder it replaced.
            if offset % 4 != 0 {
                return Err(format!("atomic offset {offset} is not 4-byte aligned"));
            }
            // SAFETY: The base address is page-aligned by `mmap` and `offset`
            // is a multiple of 4, so the resulting address is aligned for
            // `AtomicU32`. Bounds are checked above, and `AtomicU32` has the
            // same layout as the four bytes it covers. Sharing the reference is
            // sound precisely because every access through it is atomic.
            Ok(unsafe { &*(self.ptr.add(offset).cast::<AtomicU32>()) })
        }
    }

    impl Drop for SharedMapping {
        fn drop(&mut self) {
            // SAFETY: `ptr`/`len` are the exact pair returned by the `mmap` in
            // `open`, this is the only place they are unmapped, and the handle
            // is being destroyed so no borrow of the region outlives the call.
            unsafe {
                libc::munmap(self.ptr.cast::<libc::c_void>(), self.len);
            }
        }
    }
}

#[cfg(unix)]
pub use unix::SharedMapping;

// Both attributes, and `cfg(test)` on its own line rather than `cfg(all(test,
// unix))`: the runtime practices check treats a bare `#[cfg(test)]` line as the
// end of production source, and folding the two together hides the test module
// from it — every assertion below would then be scanned as a production path.
#[cfg(test)]
#[cfg(unix)]
mod tests {
    use super::SharedMapping;
    use std::io::Write;
    use std::sync::atomic::Ordering;

    fn temp_file(bytes: usize) -> tempfile::NamedTempFile {
        let mut file = match tempfile::NamedTempFile::new() {
            Ok(file) => file,
            Err(error) => panic!("temp file: {error}"),
        };
        if let Err(error) = file.write_all(&vec![0_u8; bytes]) {
            panic!("size temp file: {error}");
        }
        file
    }

    #[test]
    fn a_write_through_the_mapping_is_visible_through_the_file() {
        let file = temp_file(4096);
        let mut mapping = match SharedMapping::open(file.path(), 4096) {
            Ok(mapping) => mapping,
            Err(error) => panic!("open mapping: {error}"),
        };
        mapping.as_mut_slice()[..4].copy_from_slice(&[1, 2, 3, 4]);

        let from_file = match std::fs::read(file.path()) {
            Ok(bytes) => bytes,
            Err(error) => panic!("read back: {error}"),
        };
        assert_eq!(&from_file[..4], &[1, 2, 3, 4]);
    }

    #[test]
    fn two_mappings_of_one_file_observe_each_others_stores() {
        // The property the whole transport rests on: this is what pread/pwrite
        // was standing in for, and why it is no longer needed.
        let file = temp_file(4096);
        let mut host = match SharedMapping::open(file.path(), 4096) {
            Ok(mapping) => mapping,
            Err(error) => panic!("open host mapping: {error}"),
        };
        let kernel = match SharedMapping::open(file.path(), 4096) {
            Ok(mapping) => mapping,
            Err(error) => panic!("open kernel mapping: {error}"),
        };

        host.as_mut_slice()[64] = 0xAB;
        assert_eq!(kernel.as_slice()[64], 0xAB);
    }

    #[test]
    fn an_atomic_word_is_shared_between_mappings_of_one_file() {
        let file = temp_file(4096);
        let host = match SharedMapping::open(file.path(), 4096) {
            Ok(mapping) => mapping,
            Err(error) => panic!("open host mapping: {error}"),
        };
        let kernel = match SharedMapping::open(file.path(), 4096) {
            Ok(mapping) => mapping,
            Err(error) => panic!("open kernel mapping: {error}"),
        };

        let published = match host.atomic_u32(0) {
            Ok(word) => word,
            Err(error) => panic!("host epoch: {error}"),
        };
        let observed = match kernel.atomic_u32(0) {
            Ok(word) => word,
            Err(error) => panic!("kernel epoch: {error}"),
        };
        published.store(7, Ordering::Release);
        assert_eq!(observed.load(Ordering::Acquire), 7);
    }

    #[test]
    fn mapping_more_than_the_file_holds_is_refused() {
        // Left to mmap this succeeds and the first access raises SIGBUS, which
        // is a crash with no diagnostic rather than an error anyone can act on.
        let file = temp_file(64);
        let err = SharedMapping::open(file.path(), 4096)
            .err()
            .unwrap_or_else(|| panic!("short file must not map"));
        assert!(err.contains("64 bytes"), "unexpected error: {err}");
    }

    #[test]
    fn an_unaligned_or_out_of_bounds_atomic_is_refused() {
        let file = temp_file(4096);
        let mapping = match SharedMapping::open(file.path(), 4096) {
            Ok(mapping) => mapping,
            Err(error) => panic!("open mapping: {error}"),
        };
        assert!(mapping.atomic_u32(2).is_err(), "unaligned offset must fail");
        assert!(mapping.atomic_u32(4094).is_err(), "offset past the end must fail");
    }
}
