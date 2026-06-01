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
        Self {
            ptr,
            size,
            _marker: std::marker::PhantomData,
        }
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
