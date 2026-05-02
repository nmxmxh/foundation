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
    pub unsafe fn from_ptr(ptr: *mut u8) -> Self {
        let size_ptr = ptr.add(8) as *const u32;
        let size = *size_ptr;
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

        let write_offset_atomic = unsafe { &*(self.ptr as *const AtomicU32) };
        let mut write_offset = write_offset_atomic.load(Ordering::Acquire);

        // Ensure we have enough space (simple wrap-around)
        if write_offset + total_needed > self.size {
            write_offset = 0;
            let wrap_count_atomic = unsafe { &*(self.ptr.add(12) as *const AtomicU32) };
            wrap_count_atomic.fetch_add(1, Ordering::SeqCst);
        }

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
