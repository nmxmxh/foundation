use std::env;

use crate::NativeRuntimeHost;

pub fn serve_transport(host: &NativeRuntimeHost) -> Result<(), String> {
    match env::var("OVRT_RUNTIME_TRANSPORT") {
        Ok(mode) if mode.trim().eq_ignore_ascii_case("shm-epoch") => {
            serve_shared_memory_epoch(host)
        }
        Ok(mode) if mode.trim().eq_ignore_ascii_case("shm") => serve_shared_memory(host),
        _ => crate::serve_stdio(host),
    }
}

/// Serves exchanges with no pipe in the hot path at all.
///
/// The same mapping as `serve_shared_memory`; only the doorbell differs. See
/// `epoch_transport` for the protocol and for why stdin is still held open.
#[cfg(unix)]
fn serve_shared_memory_epoch(host: &NativeRuntimeHost) -> Result<(), String> {
    use std::path::PathBuf;
    use std::sync::atomic::Ordering;

    use ovrt_core::{SharedMapping, WaitPolicy, BUFFER_TOTAL_BYTES};

    let path = env::var("OVRT_SHM_PATH").map_err(|_| "OVRT_SHM_PATH is required".to_string())?;
    let mut mapping = SharedMapping::open(&PathBuf::from(path), BUFFER_TOTAL_BYTES as usize)?;

    let mut policy = WaitPolicy::default();
    if let Ok(spin) = env::var("OVRT_EPOCH_SPIN") {
        if let Ok(parsed) = spin.trim().parse::<u32>() {
            policy.spin_iterations = parsed;
        }
    }

    let worker = std::thread::current();
    let alive = crate::epoch_transport::spawn_host_liveness_watch(worker);
    crate::epoch_transport::serve_epoch_loop(host, &mut mapping, &policy, || {
        alive.load(Ordering::Acquire)
    })
}

#[cfg(not(unix))]
fn serve_shared_memory_epoch(_host: &NativeRuntimeHost) -> Result<(), String> {
    Err("epoch transport requires a unix platform".to_string())
}

/// Serves exchanges over the control buffer the host mapped for us.
///
/// The host maps this segment (`syscall.Mmap`, `MAP_SHARED`) and so do we, so
/// the buffer is genuinely shared: the exchange moves no bytes. This loop used
/// to open the same file and `pread` 4 KiB into a freshly allocated `Vec`,
/// process it, and `pwrite` 4 KiB back — two syscalls and an allocation per
/// call for a region that was already addressable, and, worse, a mapped writer
/// against a positional reader, which is the coherence mismatch documented at
/// `runtimehost/shared_memory_unix.go`. Mapping both ends removes both.
///
/// The doorbell is still stdio: a unit-id frame in, an empty ack out. That is
/// now the entire cost of an exchange, and it is a large one — two context
/// switches for what the epoch slots in the buffer header already describe.
/// Replacing it is the next step and deliberately not this one; keeping the
/// pipe here means the wire protocol is unchanged and the host needs no edit.
#[cfg(unix)]
fn serve_shared_memory(host: &NativeRuntimeHost) -> Result<(), String> {
    use std::io;
    use std::path::PathBuf;

    use ovrt_core::{SharedMapping, BUFFER_TOTAL_BYTES};

    let path = env::var("OVRT_SHM_PATH").map_err(|_| "OVRT_SHM_PATH is required".to_string())?;
    let mut mapping = SharedMapping::open(&PathBuf::from(path), BUFFER_TOTAL_BYTES as usize)?;

    let stdin = io::stdin();
    let stdout = io::stdout();
    let mut reader = stdin.lock();
    let mut writer = stdout.lock();

    // Allocated once. A steady-state exchange must not touch the allocator:
    // the unit id lands here and the buffer is processed where it already is.
    let mut unit_id_bytes = Vec::with_capacity(64);

    loop {
        if !crate::stdio::read_unit_frame_into(&mut reader, &mut unit_id_bytes)? {
            return Ok(());
        }
        let unit_id = std::str::from_utf8(&unit_id_bytes)
            .map_err(|error| format!("unit id is not utf-8: {error}"))?;
        crate::stdio::process_runtime_buffer_in_place(host, unit_id, mapping.as_mut_slice())?;
        crate::stdio::write_ack_frame(&mut writer)?;
    }
}

#[cfg(not(unix))]
fn serve_shared_memory(_host: &NativeRuntimeHost) -> Result<(), String> {
    Err("shared memory transport requires a unix platform".to_string())
}
