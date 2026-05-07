#[cfg(not(target_arch = "wasm32"))]
use once_cell::sync::Lazy;
#[cfg(not(target_arch = "wasm32"))]
use std::collections::HashMap;
#[cfg(not(target_arch = "wasm32"))]
use std::sync::Mutex;
#[cfg(not(target_arch = "wasm32"))]
use std::time::{SystemTime, UNIX_EPOCH};

#[cfg(target_arch = "wasm32")]
#[link(wasm_import_module = "env")]
extern "C" {
    fn ovrt_get_byte_length(handle: u32) -> u32;
    fn ovrt_copy_to_buffer(handle: u32, target_offset: u32, src_ptr: *const u8, len: u32);
    fn ovrt_copy_from_buffer(handle: u32, src_offset: u32, dest_ptr: *mut u8, len: u32);
    fn ovrt_atomic_load(handle: u32, index: u32) -> i32;
    fn ovrt_atomic_store(handle: u32, index: u32, value: i32) -> i32;
    fn ovrt_atomic_add(handle: u32, index: u32, delta: i32) -> i32;
    fn ovrt_atomic_compare_exchange(
        handle: u32,
        index: u32,
        expected: i32,
        replacement: i32,
    ) -> i32;
    fn ovrt_atomic_notify(handle: u32, index: u32, count: i32) -> i32;
    fn ovrt_log(ptr: *const u8, len: u32, level: u8);
    fn ovrt_get_now() -> f64;
    fn ovrt_fill_random(ptr: *mut u8, len: u32);
}

#[cfg(not(target_arch = "wasm32"))]
static BUFFERS: Lazy<Mutex<HashMap<u32, Vec<u8>>>> = Lazy::new(|| Mutex::new(HashMap::new()));

#[cfg(not(target_arch = "wasm32"))]
fn with_buffers<T>(operation: impl FnOnce(&HashMap<u32, Vec<u8>>) -> T) -> T {
    match BUFFERS.lock() {
        Ok(guard) => operation(&guard),
        Err(poisoned) => {
            let guard = poisoned.into_inner();
            operation(&guard)
        }
    }
}

#[cfg(not(target_arch = "wasm32"))]
fn with_buffers_mut<T>(operation: impl FnOnce(&mut HashMap<u32, Vec<u8>>) -> T) -> T {
    match BUFFERS.lock() {
        Ok(mut guard) => operation(&mut guard),
        Err(poisoned) => {
            let mut guard = poisoned.into_inner();
            operation(&mut guard)
        }
    }
}

#[cfg(not(target_arch = "wasm32"))]
fn read_i32(buffer: &[u8], start: usize) -> Option<i32> {
    let end = start.checked_add(4)?;
    let bytes = buffer.get(start..end)?;
    let array: [u8; 4] = bytes.try_into().ok()?;
    Some(i32::from_le_bytes(array))
}

#[cfg(not(target_arch = "wasm32"))]
pub fn create_mock_buffer(size: usize) -> u32 {
    with_buffers_mut(|buffers| {
        let handle = buffers.len() as u32 + 1;
        buffers.insert(handle, vec![0; size]);
        handle
    })
}

#[cfg(target_arch = "wasm32")]
pub fn create_mock_buffer(_size: usize) -> u32 {
    0
}

pub fn get_byte_length(handle: u32) -> u32 {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_get_byte_length(handle)
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        with_buffers(|buffers| {
            buffers
                .get(&handle)
                .map(|buffer| buffer.len() as u32)
                .unwrap_or(0)
        })
    }
}

pub fn copy_to_buffer(handle: u32, target_offset: u32, src: &[u8]) {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_copy_to_buffer(handle, target_offset, src.as_ptr(), src.len() as u32);
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        with_buffers_mut(|buffers| {
            if let Some(buffer) = buffers.get_mut(&handle) {
                let start = target_offset as usize;
                let end = start.saturating_add(src.len());
                if end <= buffer.len() {
                    buffer[start..end].copy_from_slice(src);
                }
            }
        });
    }
}

pub fn copy_from_buffer(handle: u32, src_offset: u32, dest: &mut [u8]) {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_copy_from_buffer(handle, src_offset, dest.as_mut_ptr(), dest.len() as u32);
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        with_buffers(|buffers| {
            if let Some(buffer) = buffers.get(&handle) {
                let start = src_offset as usize;
                let end = start.saturating_add(dest.len());
                if end <= buffer.len() {
                    dest.copy_from_slice(&buffer[start..end]);
                }
            }
        });
    }
}

pub fn atomic_load(handle: u32, index: u32) -> i32 {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_atomic_load(handle, index)
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        with_buffers(|buffers| {
            buffers
                .get(&handle)
                .and_then(|buffer| read_i32(buffer, index as usize * 4))
                .unwrap_or(0)
        })
    }
}

pub fn atomic_store(handle: u32, index: u32, value: i32) -> i32 {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_atomic_store(handle, index, value)
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        with_buffers_mut(|buffers| {
            if let Some(buffer) = buffers.get_mut(&handle) {
                let start = index as usize * 4;
                let end = start.saturating_add(4);
                if end <= buffer.len() {
                    let old = read_i32(buffer, start).unwrap_or(0);
                    buffer[start..end].copy_from_slice(&value.to_le_bytes());
                    return old;
                }
            }
            0
        })
    }
}

pub fn atomic_add(handle: u32, index: u32, delta: i32) -> i32 {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_atomic_add(handle, index, delta)
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        with_buffers_mut(|buffers| {
            if let Some(buffer) = buffers.get_mut(&handle) {
                let start = index as usize * 4;
                let end = start.saturating_add(4);
                if end <= buffer.len() {
                    let old = read_i32(buffer, start).unwrap_or(0);
                    let next = old.wrapping_add(delta);
                    buffer[start..end].copy_from_slice(&next.to_le_bytes());
                    return old;
                }
            }
            0
        })
    }
}

pub fn atomic_compare_exchange(handle: u32, index: u32, expected: i32, replacement: i32) -> i32 {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_atomic_compare_exchange(handle, index, expected, replacement)
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        with_buffers_mut(|buffers| {
            if let Some(buffer) = buffers.get_mut(&handle) {
                let start = index as usize * 4;
                let end = start.saturating_add(4);
                if end <= buffer.len() {
                    let old = read_i32(buffer, start).unwrap_or(0);
                    if old == expected {
                        buffer[start..end].copy_from_slice(&replacement.to_le_bytes());
                    }
                    return old;
                }
            }
            0
        })
    }
}

pub fn atomic_notify(handle: u32, index: u32, count: i32) -> i32 {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_atomic_notify(handle, index, count)
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        let _ = (handle, index, count);
        1
    }
}

pub fn console_log(message: &str, level: u8) {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_log(message.as_ptr(), message.len() as u32, level);
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        let prefix = match level {
            0 => "ERROR",
            1 => "WARN",
            2 => "INFO",
            _ => "DEBUG",
        };
        eprintln!("[{prefix}] {message}");
    }
}

pub fn get_now() -> f64 {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_get_now()
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|duration| duration.as_secs_f64() * 1000.0)
            .unwrap_or(0.0)
    }
}

pub fn fill_random(bytes: &mut [u8]) {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        ovrt_fill_random(bytes.as_mut_ptr(), bytes.len() as u32);
    }

    #[cfg(not(target_arch = "wasm32"))]
    {
        // Deterministic host fallback for repeatable native tests. This is not
        // cryptographic randomness and must not be reused for security tokens.
        for (index, byte) in bytes.iter_mut().enumerate() {
            *byte = (index % u8::MAX as usize) as u8;
        }
    }
}
