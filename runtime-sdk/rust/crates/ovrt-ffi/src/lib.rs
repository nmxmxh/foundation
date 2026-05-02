#![allow(unsafe_code)]

use std::ffi::{c_char, c_void};
use std::slice;

use ovrt_native::{process_runtime_buffer_in_place, NativeRuntimeHost};

pub const ABI_VERSION: u32 = 1;

/// # Safety
///
/// `out_host` must be a valid writable pointer to storage for one host handle.
/// `err_buf` must either be null or point to a writable buffer of `err_cap` bytes.
pub unsafe fn create_host<F>(
    workers: usize,
    out_host: *mut *mut c_void,
    err_buf: *mut c_char,
    err_cap: usize,
    builder: F,
) -> i32
where
    F: FnOnce(usize) -> Result<NativeRuntimeHost, String>,
{
    if out_host.is_null() {
        write_error(err_buf, err_cap, "ffi output host pointer is required");
        return 1;
    }

    match builder(workers.max(1)) {
        Ok(host) => {
            let boxed = Box::new(host);
            *out_host = Box::into_raw(boxed) as *mut c_void;
            0
        }
        Err(error) => {
            write_error(err_buf, err_cap, &error);
            1
        }
    }
}

/// # Safety
///
/// `host` must either be null or a handle previously returned by `create_host`
/// that has not already been destroyed.
pub unsafe fn destroy_host(host: *mut c_void) {
    if host.is_null() {
        return;
    }
    drop(Box::from_raw(host as *mut NativeRuntimeHost));
}

/// # Safety
///
/// `host` must be a live handle returned by `create_host`. `unit_id_ptr` must
/// reference `unit_id_len` bytes of valid UTF-8 for the duration of the call.
/// `buffer_ptr` must reference `buffer_len` writable bytes for the duration of
/// the call. `err_buf` must either be null or point to a writable buffer of
/// `err_cap` bytes.
pub unsafe fn process_buffer(
    host: *mut c_void,
    unit_id_ptr: *const u8,
    unit_id_len: usize,
    buffer_ptr: *mut u8,
    buffer_len: usize,
    err_buf: *mut c_char,
    err_cap: usize,
) -> i32 {
    if host.is_null() {
        write_error(err_buf, err_cap, "ffi runtime host is nil");
        return 1;
    }
    if unit_id_ptr.is_null() {
        write_error(err_buf, err_cap, "ffi runtime unit id pointer is nil");
        return 1;
    }
    if buffer_ptr.is_null() {
        write_error(err_buf, err_cap, "ffi runtime buffer pointer is nil");
        return 1;
    }

    let unit_id = slice::from_raw_parts(unit_id_ptr, unit_id_len);
    let unit_id = match std::str::from_utf8(unit_id) {
        Ok(unit_id) => unit_id,
        Err(error) => {
            write_error(err_buf, err_cap, &error.to_string());
            return 1;
        }
    };

    let buffer = slice::from_raw_parts_mut(buffer_ptr, buffer_len);
    let host = &*(host as *mut NativeRuntimeHost);
    match process_runtime_buffer_in_place(host, unit_id, buffer) {
        Ok(()) => 0,
        Err(error) => {
            write_error(err_buf, err_cap, &error);
            1
        }
    }
}

pub fn write_error(err_buf: *mut c_char, err_cap: usize, message: &str) {
    if err_buf.is_null() || err_cap == 0 {
        return;
    }
    let bytes = message.as_bytes();
    let copy_len = utf8_prefix_len(message, err_cap.saturating_sub(1));
    unsafe {
        let target = err_buf as *mut u8;
        std::ptr::copy_nonoverlapping(bytes.as_ptr(), target, copy_len);
        *target.add(copy_len) = 0;
    }
}

fn utf8_prefix_len(message: &str, max_bytes: usize) -> usize {
    if message.len() <= max_bytes {
        return message.len();
    }
    let mut copy_len = 0;
    for (index, character) in message.char_indices() {
        let next_len = index + character.len_utf8();
        if next_len > max_bytes {
            break;
        }
        copy_len = next_len;
    }
    copy_len
}

#[macro_export]
macro_rules! export_runtime_ffi {
    ($builder:path) => {
        #[no_mangle]
        pub extern "C" fn ovrt_runtime_abi_version() -> u32 {
            $crate::ABI_VERSION
        }

        #[no_mangle]
        pub unsafe extern "C" fn ovrt_runtime_create(
            workers: usize,
            out_host: *mut *mut ::std::ffi::c_void,
            err_buf: *mut ::std::ffi::c_char,
            err_cap: usize,
        ) -> i32 {
            $crate::create_host(workers, out_host, err_buf, err_cap, $builder)
        }

        #[no_mangle]
        pub unsafe extern "C" fn ovrt_runtime_destroy(host: *mut ::std::ffi::c_void) {
            $crate::destroy_host(host);
        }

        #[no_mangle]
        pub unsafe extern "C" fn ovrt_runtime_process_buffer(
            host: *mut ::std::ffi::c_void,
            unit_id_ptr: *const u8,
            unit_id_len: usize,
            buffer_ptr: *mut u8,
            buffer_len: usize,
            err_buf: *mut ::std::ffi::c_char,
            err_cap: usize,
        ) -> i32 {
            $crate::process_buffer(
                host,
                unit_id_ptr,
                unit_id_len,
                buffer_ptr,
                buffer_len,
                err_buf,
                err_cap,
            )
        }
    };
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;
    use std::sync::Arc;

    use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor, BUFFER_TOTAL_BYTES};
    use ovrt_unit::RuntimeUnit;

    use super::*;

    struct EchoUnit;

    impl RuntimeUnit for EchoUnit {
        fn descriptor(&self) -> RuntimeUnitDescriptor {
            RuntimeUnitDescriptor {
                unit_id: "ffi.echo".to_string(),
                role: RuntimeRole::Compute,
                input_schema: "common/v1/envelope.capnp".to_string(),
                output_schema: "common/v1/envelope.capnp".to_string(),
                supports_wasm: false,
                supports_native: true,
                requires_shared_memory: false,
                supports_gpu: false,
                max_concurrency: 1,
            }
        }

        fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
            Ok(input
                .iter()
                .map(|value| value.to_ascii_uppercase())
                .collect())
        }
    }

    fn build_host(_workers: usize) -> Result<NativeRuntimeHost, String> {
        let host = NativeRuntimeHost::new(BTreeMap::new());
        host.register_unit(Arc::new(EchoUnit))?;
        Ok(host)
    }

    #[test]
    fn creates_and_processes_runtime_buffers() {
        let mut raw_host: *mut c_void = std::ptr::null_mut();
        let mut error = [0_i8; 256];
        assert_eq!(
            unsafe {
                create_host(
                    1,
                    &mut raw_host,
                    error.as_mut_ptr(),
                    error.len(),
                    build_host,
                )
            },
            0
        );
        assert!(!raw_host.is_null());

        let mut buffer = vec![0_u8; BUFFER_TOTAL_BYTES as usize];
        let mut runtime_buffer = ovrt_native::NativeBuffer::new(buffer.clone()).expect("buffer");
        runtime_buffer.initialize_control_plane(1).expect("init");
        runtime_buffer
            .write_input_bytes(b"ffi")
            .expect("write input");
        buffer.copy_from_slice(runtime_buffer.into_inner().as_slice());

        let mut error = [0_i8; 256];
        assert_eq!(
            unsafe {
                process_buffer(
                    raw_host,
                    b"ffi.echo".as_ptr(),
                    "ffi.echo".len(),
                    buffer.as_mut_ptr(),
                    buffer.len(),
                    error.as_mut_ptr(),
                    error.len(),
                )
            },
            0
        );

        let runtime_buffer = ovrt_native::NativeBuffer::new(buffer).expect("buffer");
        assert_eq!(runtime_buffer.read_output_bytes().expect("output"), b"FFI");

        unsafe {
            destroy_host(raw_host);
        }
    }

    #[test]
    fn write_error_truncates_at_utf8_boundary() {
        let mut error = [0_i8; 8];
        write_error(error.as_mut_ptr(), error.len(), "err 🧑‍💻");

        let bytes = error
            .iter()
            .take_while(|byte| **byte != 0)
            .map(|byte| *byte as u8)
            .collect::<Vec<_>>();
        let message = std::str::from_utf8(&bytes).expect("error must remain valid utf-8");
        assert_eq!(message, "err ");
    }

    #[test]
    fn handles_parallel_process_buffer_calls_on_shared_host() {
        let mut raw_host: *mut c_void = std::ptr::null_mut();
        let mut error = [0_i8; 256];
        assert_eq!(
            unsafe {
                create_host(
                    4,
                    &mut raw_host,
                    error.as_mut_ptr(),
                    error.len(),
                    build_host,
                )
            },
            0
        );

        let host_address = raw_host as usize;
        let handles = (0..8)
            .map(|index| {
                std::thread::spawn(move || {
                    let raw_host = host_address as *mut c_void;
                    let input = format!("ffi-{index}");
                    let expected = input.to_ascii_uppercase();
                    let mut buffer = vec![0_u8; BUFFER_TOTAL_BYTES as usize];
                    let mut runtime_buffer =
                        ovrt_native::NativeBuffer::new(buffer.clone()).expect("buffer");
                    runtime_buffer.initialize_control_plane(1).expect("init");
                    runtime_buffer
                        .write_input_bytes(input.as_bytes())
                        .expect("write input");
                    buffer.copy_from_slice(runtime_buffer.into_inner().as_slice());

                    let mut error = [0_i8; 256];
                    assert_eq!(
                        unsafe {
                            process_buffer(
                                raw_host,
                                b"ffi.echo".as_ptr(),
                                "ffi.echo".len(),
                                buffer.as_mut_ptr(),
                                buffer.len(),
                                error.as_mut_ptr(),
                                error.len(),
                            )
                        },
                        0
                    );

                    let runtime_buffer = ovrt_native::NativeBuffer::new(buffer).expect("buffer");
                    assert_eq!(
                        runtime_buffer.read_output_bytes().expect("output"),
                        expected.as_bytes()
                    );
                })
            })
            .collect::<Vec<_>>();

        for handle in handles {
            handle.join().expect("worker thread must finish");
        }

        unsafe {
            destroy_host(raw_host);
        }
    }
}
