//! Guest-owned regions shared with the JavaScript host through linear memory.
//! The host must publish ownership before access and release borrowed views before freeing a region.

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicI32, Ordering};
use std::sync::Mutex;

use once_cell::sync::Lazy;

pub const LINEAR_HANDLE_START: u32 = 0x8000_0000;
const MAX_REGIONS: usize = 64;
const MAX_BYTES: usize = 64 * 1024 * 1024;
const MAX_ALLOCATED_BYTES: usize = 96 * 1024 * 1024;

struct Region {
    words: Vec<u64>,
    length: usize,
}

struct Regions {
    entries: BTreeMap<u32, Region>,
    next_handle: u32,
    allocated: usize,
}

static REGIONS: Lazy<Mutex<Regions>> = Lazy::new(|| {
    Mutex::new(Regions { entries: BTreeMap::new(), next_handle: LINEAR_HANDLE_START, allocated: 0 })
});

fn with_regions<T>(operation: impl FnOnce(&mut Regions) -> Result<T, String>) -> Result<T, String> {
    let mut regions = REGIONS.try_lock().map_err(|_| "linear buffer is busy".to_string())?;
    operation(&mut regions)
}

#[no_mangle]
pub extern "C" fn ovrt_buffer_abi_version() -> u32 {
    2
}

#[no_mangle]
pub extern "C" fn ovrt_buffer_alloc(length: u32) -> u32 {
    allocate(length as usize).unwrap_or(0)
}

fn allocate(length: usize) -> Result<u32, String> {
    if length == 0 || length > MAX_BYTES {
        return Err("linear buffer size exceeds budget".into());
    }
    with_regions(|regions| {
        let allocated = (length + 7) & !7;
        if regions.entries.len() >= MAX_REGIONS
            || regions.allocated + allocated > MAX_ALLOCATED_BYTES
        {
            return Err("linear buffer budget exhausted".into());
        }
        let handle = regions.next_handle;
        let next_handle = handle.checked_add(1).ok_or("linear buffer handles exhausted")?;
        let mut words = Vec::new();
        words.try_reserve_exact(allocated / 8).map_err(|_| "linear buffer allocation failed")?;
        words.resize(allocated / 8, 0u64);
        regions.entries.insert(handle, Region { words, length });
        regions.allocated += allocated;
        regions.next_handle = next_handle;
        Ok(handle)
    })
}

#[no_mangle]
pub extern "C" fn ovrt_buffer_ptr(handle: u32) -> usize {
    with_regions(|regions| {
        let region = regions.entries.get(&handle).ok_or("unknown linear buffer")?;
        Ok(region.words.as_ptr() as usize)
    })
    .unwrap_or(0)
}

#[no_mangle]
pub extern "C" fn ovrt_buffer_free(handle: u32) -> i32 {
    with_regions(|regions| {
        let region = regions.entries.remove(&handle).ok_or("unknown linear buffer")?;
        regions.allocated -= region.words.len() * 8;
        Ok(1)
    })
    .unwrap_or(0)
}

pub fn capacity(handle: u32) -> Result<usize, String> {
    with_regions(|regions| {
        regions
            .entries
            .get(&handle)
            .map(|region| region.length)
            .ok_or("unknown linear buffer".into())
    })
}

pub fn with_bytes<T>(
    handle: u32,
    offset: u32,
    length: u32,
    read: impl FnOnce(&[u8]) -> T,
) -> Result<T, String> {
    with_regions(|regions| {
        let region = regions.entries.get(&handle).ok_or("unknown linear buffer")?;
        ovrt_core::layout::validate_region(offset, length, region.length)?;
        // SAFETY: The registry pins this allocation. The checked region stays borrowed only during this callback.
        // The host owns publication and must not mutate these bytes until the guest returns.
        let bytes = unsafe {
            std::slice::from_raw_parts(
                region.words.as_ptr().cast::<u8>().add(offset as usize),
                length as usize,
            )
        };
        Ok(read(bytes))
    })
}

pub fn write(handle: u32, offset: u32, bytes: &[u8]) -> Result<(), String> {
    with_regions(|regions| {
        let region = regions.entries.get_mut(&handle).ok_or("unknown linear buffer")?;
        let length = u32::try_from(bytes.len()).map_err(|_| "linear buffer write is too large")?;
        ovrt_core::layout::validate_region(offset, length, region.length)?;
        // SAFETY: The registry pins the checked destination. The host must not access it during guest execution.
        unsafe {
            std::ptr::copy(
                bytes.as_ptr(),
                region.words.as_mut_ptr().cast::<u8>().add(offset as usize),
                bytes.len(),
            )
        };
        Ok(())
    })
}

pub fn atomic(
    handle: u32,
    index: u32,
    operation: impl FnOnce(&AtomicI32) -> i32,
) -> Result<i32, String> {
    with_regions(|regions| {
        let region = regions.entries.get(&handle).ok_or("unknown linear buffer")?;
        let offset = index.checked_mul(4).ok_or("linear atomic index overflow")?;
        ovrt_core::layout::validate_region(offset, 4, region.length)?;
        // SAFETY: The allocation has eight-byte alignment. Epoch offsets have four-byte alignment and use atomic access only.
        let slot = unsafe {
            &*region.words.as_ptr().cast::<u8>().add(offset as usize).cast::<AtomicI32>()
        };
        Ok(operation(slot))
    })
}

pub fn load(handle: u32, index: u32) -> i32 {
    atomic(handle, index, |slot| slot.load(Ordering::SeqCst)).unwrap_or(0)
}

pub fn store(handle: u32, index: u32, value: i32) -> i32 {
    atomic(handle, index, |slot| {
        slot.store(value, Ordering::SeqCst);
        value
    })
    .unwrap_or(0)
}

pub fn add(handle: u32, index: u32, value: i32) -> i32 {
    atomic(handle, index, |slot| slot.fetch_add(value, Ordering::SeqCst)).unwrap_or(0)
}

pub fn compare_exchange(handle: u32, index: u32, expected: i32, replacement: i32) -> i32 {
    atomic(handle, index, |slot| {
        match slot.compare_exchange(expected, replacement, Ordering::SeqCst, Ordering::SeqCst) {
            Ok(previous) | Err(previous) => previous,
        }
    })
    .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::SafeBuffer;

    #[test]
    fn validates_lifetimes_bounds_borrowing_and_epochs() {
        assert_eq!(ovrt_buffer_alloc(0), 0);
        assert_eq!(ovrt_buffer_alloc(MAX_BYTES as u32 + 1), 0);
        let handle = ovrt_buffer_alloc(4096);
        assert!(handle >= LINEAR_HANDLE_START);
        assert_eq!(ovrt_buffer_ptr(handle) % 8, 0);
        let buffer = SafeBuffer::new(handle).expect("linear buffer");
        buffer.initialize_control_plane(3).expect("initialize");
        buffer.write_input_bytes(b"borrowed").expect("publish");
        let pointer = ovrt_buffer_ptr(handle);
        let length = buffer.with_input_bytes(|bytes| {
            assert_eq!(bytes, b"borrowed");
            assert_eq!(bytes.as_ptr() as usize, pointer + 256);
            assert!(buffer.read_at(256, 1).is_err());
            assert_eq!(ovrt_buffer_free(handle), 0);
            bytes.len()
        });
        assert_eq!(length, Ok(8));
        assert!(buffer.with_bytes(4095, 2, |_| ()).is_err());
        assert!(buffer.write_at(4095, &[1, 2]).is_err());
        assert_eq!(buffer.add_epoch(1, 1), 0);
        assert_eq!(buffer.compare_exchange_epoch(1, 1, 3), 1);
        assert_eq!(buffer.load_epoch(1), 3);
        assert_eq!(buffer.store_epoch(1, 7), 7);
        assert_eq!(buffer.load_epoch(1), 7);
        buffer.set_header_int(1, -1).expect("negative header");
        assert!(buffer.read_input_bytes().is_err());
        assert_eq!(ovrt_buffer_free(handle), 1);
        assert_eq!(ovrt_buffer_free(handle), 0);
        assert!(buffer.read_at(256, 1).is_err());
        assert!(SafeBuffer::new(handle).is_err());
    }
}
