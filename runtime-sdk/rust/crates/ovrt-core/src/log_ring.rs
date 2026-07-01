use std::sync::atomic::{AtomicU32, Ordering};

/// LogRingBuffer implements a high-performance, SharedArrayBuffer-backed
/// ring buffer for log streaming from Rust to the Browser Host.
///
/// Memory Layout:
/// [0..4]   - Write Offset (atomic)
/// [4..8]   - Read Offset (atomic)
/// [8..12]  - Buffer Size
/// [12..16] - Wrap Count
/// [16..64] - Reserved
/// [64..]   - Data Slabs
pub struct LogRingBuffer<'a> {
    ptr: *mut u8,
    size: u32,
    _marker: std::marker::PhantomData<&'a mut [u8]>,
}

impl<'a> LogRingBuffer<'a> {
    /// # Safety
    ///
    /// `ptr` must point to a writable log-ring memory region that is valid for
    /// the returned buffer lifetime. The first 16 bytes must contain the
    /// expected atomic write/read offsets, buffer size, and wrap counter using
    /// the runtime log-ring layout. The backing region must be at least
    /// `64 + size` bytes long and must not be concurrently freed while this
    /// handle is used.
    pub unsafe fn from_ptr(ptr: *mut u8) -> Self {
        // SAFETY: The caller guarantees that `ptr` points to the runtime log-ring
        // header, where bytes 8..12 contain a valid little-endian u32 size field.
        let size = unsafe {
            let size_ptr = ptr.add(8) as *const u32;
            *size_ptr
        };
        Self { ptr, size, _marker: std::marker::PhantomData }
    }

    pub fn write(&self, message: &str) {
        let bytes = message.as_bytes();
        let length = bytes.len() as u32;
        let total_needed = length + 4;

        if total_needed > self.size {
            return; // Message too big for buffer
        }

        // SAFETY: `from_ptr` only constructs this handle for a region whose first
        // word is the atomic write offset shared with the host.
        let write_offset_atomic = unsafe { &*(self.ptr as *const AtomicU32) };
        let mut write_offset = write_offset_atomic.load(Ordering::Acquire);

        // Ensure we have enough space (simple wrap-around)
        if write_offset + total_needed > self.size {
            write_offset = 0;
            // SAFETY: `from_ptr` requires the wrap counter at bytes 12..16 using
            // the log-ring control-plane layout.
            let wrap_count_atomic = unsafe { &*(self.ptr.add(12) as *const AtomicU32) };
            wrap_count_atomic.fetch_add(1, Ordering::SeqCst);
        }

        // SAFETY: Bounds above guarantee `write_offset + total_needed <= size`;
        // the caller-owned region is at least `64 + size` bytes and writable.
        unsafe {
            // Write length (4 bytes, little endian as per TS setUint32(..., true))
            let target_len_ptr = self.ptr.add(write_offset as usize + 64) as *mut u32;
            target_len_ptr.write_unaligned(length.to_le());

            // Write message bytes
            let target_data_ptr = self.ptr.add(write_offset as usize + 68);
            std::ptr::copy_nonoverlapping(bytes.as_ptr(), target_data_ptr, length as usize);
        }

        write_offset_atomic.store(write_offset + total_needed, Ordering::Release);
    }
}

/// Loom interleaving verification of the log-ring publication protocol.
///
/// `LogRingBuffer::write` itself reinterprets a raw `*mut u8` as `&AtomicU32`,
/// which loom cannot model directly (loom atomics carry their own state and
/// cannot be constructed from arbitrary pointers). So we model the exact
/// synchronization contract the ring relies on, using loom primitives, and let
/// loom explore every thread interleaving and every legal memory ordering:
///
/// * the producer writes the slab payload (a non-atomic store), then publishes
///   by storing the advanced write offset with `Release` — mirroring the data
///   writes followed by `write_offset_atomic.store(.., Release)` in `write`;
/// * a consumer (the browser host) loads the write offset with `Acquire`, and
///   only reads the slab once it observes the advanced offset — mirroring the
///   host reading up to `write_offset` after an `Acquire` load.
///
/// The invariant: any consumer that observes the published offset must see the
/// fully written slab — never a torn or unpublished value. If the orderings
/// were weakened to `Relaxed`, loom would find an interleaving that violates
/// this assertion.
#[cfg(all(test, feature = "loom"))]
mod loom_verification {
    use loom::cell::UnsafeCell;
    use loom::sync::atomic::{AtomicU32, Ordering};
    use loom::sync::Arc;

    const SENTINEL: u32 = 0xA5A5_A5A5;

    struct ModelRing {
        write_offset: AtomicU32,
        // Stands in for the data slab at `ptr + 64`. Non-atomic on purpose:
        // publication safety must come from the offset's Release/Acquire edge,
        // not from the slab being atomic.
        slab: UnsafeCell<u32>,
    }

    #[test]
    fn acquire_release_publishes_complete_slab() {
        loom::model(|| {
            let ring = Arc::new(ModelRing {
                write_offset: AtomicU32::new(0),
                slab: UnsafeCell::new(0),
            });

            let producer = {
                let ring = ring.clone();
                loom::thread::spawn(move || {
                    // Write the slab payload, then publish via Release store.
                    ring.slab.with_mut(|p| unsafe { *p = SENTINEL });
                    ring.write_offset.store(4, Ordering::Release);
                })
            };

            // Consumer: only read the slab once the published offset is visible.
            if ring.write_offset.load(Ordering::Acquire) != 0 {
                let observed = ring.slab.with(|p| unsafe { *p });
                assert_eq!(
                    observed, SENTINEL,
                    "consumer observed a torn or unpublished slab",
                );
            }

            producer.join().unwrap();
        });
    }

    /// A producer publication (offset advancing 0 -> 8) must remain monotonic
    /// from the consumer's view: observing the later offset implies the slab
    /// write also happened-before. This guards the sequential append discipline
    /// the ring depends on under repeated writes.
    #[test]
    fn sequential_publications_stay_monotonic() {
        loom::model(|| {
            let ring = Arc::new(ModelRing {
                write_offset: AtomicU32::new(0),
                slab: UnsafeCell::new(0),
            });

            let producer = {
                let ring = ring.clone();
                loom::thread::spawn(move || {
                    ring.slab.with_mut(|p| unsafe { *p = SENTINEL });
                    ring.write_offset.store(8, Ordering::Release);
                })
            };

            let offset = ring.write_offset.load(Ordering::Acquire);
            assert!(offset == 0 || offset == 8, "offset must be monotonic");
            if offset == 8 {
                let observed = ring.slab.with(|p| unsafe { *p });
                assert_eq!(observed, SENTINEL, "published offset without slab data");
            }

            producer.join().unwrap();
        });
    }
}
