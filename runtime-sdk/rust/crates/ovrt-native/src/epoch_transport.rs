//! The kernel half of the epoch doorbell.
//!
//! The `shm` transport moves no bytes but still costs two context switches: the
//! host writes a unit-id frame down a pipe and blocks reading an
//! acknowledgement. Measured, that pipe is essentially the entire cost of a
//! crossing — the copies it used to hide are gone.
//!
//! This loop removes it. The host writes the route and the payload into the
//! mapping, then increments `IDX_INPUT_WRITTEN`; that store *is* the crossing.
//! This side observes the change, runs the unit in place, and increments
//! `IDX_OUTPUT_WRITTEN`. Nothing is sent, nothing is copied, and neither
//! process enters the kernel on a warm exchange.
//!
//! What the pipe was also doing, and what replaces it:
//!
//! - **A barrier.** A pipe read orders everything before it, so the old design
//!   could not observe a half-written buffer. Here the ordering is explicit:
//!   `ovrt_core::epoch::publish` is a release store and `observe` is an acquire
//!   load. Remove either and a reader sees a descriptor that is addressable but
//!   not yet filled.
//! - **Liveness.** A dead host closed stdin and `read_frame` reached EOF. With
//!   no read in the hot path, a dead host and a slow one look identical from the
//!   slot. `stdin` is therefore still held open and still watched — off the hot
//!   path, on a supervisor thread, purely so EOF still means something.

use std::sync::atomic::AtomicU32;
use std::sync::Arc;

use ovrt_core::epoch::{self, WaitError, WaitPolicy};
use ovrt_core::{
    SharedMapping, BUFFER_TOTAL_BYTES, EPOCH_SLOT_BYTES, IDX_INPUT_WRITTEN, IDX_KERNEL_READY,
    IDX_OUTPUT_WRITTEN, OFFSET_EPOCHS, OFFSET_ROUTE_BYTES, ROUTE_MAX_BYTES,
};

use crate::stdio::process_runtime_buffer_unpublished;
use crate::NativeRuntimeHost;

fn slot_offset(index: u32) -> usize {
    (OFFSET_EPOCHS + index * EPOCH_SLOT_BYTES) as usize
}

/// Reads the unit id the host wrote into the buffer's route region.
///
/// Trailing NULs are stripped rather than the length being carried separately:
/// the region is fixed and zeroed, so its content is self-describing, and one
/// fewer field is one fewer way for the two sides to disagree.
pub fn read_route(raw: &[u8]) -> Result<&str, String> {
    let start = OFFSET_ROUTE_BYTES as usize;
    let end = start + ROUTE_MAX_BYTES as usize;
    if end > raw.len() {
        return Err(format!("route region runs past the {} byte buffer", raw.len()));
    }
    let region = &raw[start..end];
    let length = region.iter().position(|byte| *byte == 0).unwrap_or(region.len());
    if length == 0 {
        return Err("exchange carries no route; the host published without a unit id".to_string());
    }
    std::str::from_utf8(&region[..length]).map_err(|error| format!("route is not utf-8: {error}"))
}

/// Serves exchanges over epoch slots until the host goes away.
///
/// `host_alive` is polled only while parked, never during the spin. It exists
/// because the slot alone cannot distinguish a host that is slow from one that
/// has exited, and a kernel that cannot tell the difference never exits.
pub fn serve_epoch_loop(
    runtime: &NativeRuntimeHost,
    mapping: &mut SharedMapping,
    policy: &WaitPolicy,
    mut host_alive: impl FnMut() -> bool,
) -> Result<(), String> {
    if mapping.len() < BUFFER_TOTAL_BYTES as usize {
        return Err(format!(
            "control mapping is {} bytes, want {BUFFER_TOTAL_BYTES}",
            mapping.len()
        ));
    }

    // Snapshot the input epoch *before* announcing readiness, not after.
    //
    // The other order loses the first exchange, and loses it only sometimes: the
    // host publishes its warm-up call the instant the child is spawned, so a
    // snapshot taken after READY can already include that publication, leaving
    // this loop waiting for a change that has been and gone. Taking it first
    // means every publication the host makes after seeing READY is a change
    // this side has not yet observed.
    let mut last_input = epoch::observe(mapping.atomic_u32(slot_offset(IDX_INPUT_WRITTEN))?);

    // Published last, after the mapping is validated and the units are
    // registered. A host that sees READY and finds no units would report an
    // empty kernel rather than a kernel that had not finished starting.
    let ready = mapping.atomic_u32(slot_offset(IDX_KERNEL_READY))?;
    epoch::publish_next(ready);

    loop {
        let input_slot: &AtomicU32 = mapping.atomic_u32(slot_offset(IDX_INPUT_WRITTEN))?;
        match epoch::wait_for_change(input_slot, last_input, policy, &mut host_alive) {
            Ok(observed) => last_input = observed,
            // Both are orderly shutdowns from this side: a host that exited is
            // not an error to report to nobody, and a timeout with no host
            // means the same thing more slowly.
            Err(WaitError::PeerLost) => return Ok(()),
            Err(WaitError::TimedOut) => {
                if host_alive() {
                    continue;
                }
                return Ok(());
            }
        }

        // The acquire in `wait_for_change` is what makes the rest of the buffer
        // safe to read here. Reading the route before observing the epoch would
        // race with the host writing it.
        {
            let raw = mapping.as_mut_slice();
            let route = read_route(raw)?.to_string();
            // The outcome is deliberately not branched on. It distinguishes a
            // unit that produced output from one that failed, which matters to
            // the stdio path's epoch bookkeeping and not at all here: either way
            // the host is blocked waiting, and either way what it needs — a
            // result or a status code and diagnostics — is already in the
            // buffer. Withholding the epoch on failure would leave the caller
            // waiting out its timeout for an answer that exists.
            let _ = process_runtime_buffer_unpublished(runtime, &route, raw)?;
        }

        let output_slot = mapping.atomic_u32(slot_offset(IDX_OUTPUT_WRITTEN))?;
        epoch::publish_next(output_slot);
    }
}

/// Watches stdin for EOF and reports whether the host is still there.
///
/// The pipe survives the doorbell's removal, demoted to exactly one job. It is
/// never read on the hot path — a read there is the cost this whole change
/// removes — but a host that dies still closes it, and that is the only signal
/// a kernel gets for a `SIGKILL`, which no in-band epoch can carry.
pub fn spawn_host_liveness_watch() -> Arc<std::sync::atomic::AtomicBool> {
    use std::io::Read;
    use std::sync::atomic::{AtomicBool, Ordering};

    let alive = Arc::new(AtomicBool::new(true));
    let flag = Arc::clone(&alive);
    // Detached deliberately: it outlives nothing, holds nothing, and ends when
    // the pipe does. Joining it would mean blocking shutdown on a read.
    let _ = std::thread::Builder::new().name("ovrt-host-liveness".to_string()).spawn(move || {
        let mut byte = [0_u8; 1];
        loop {
            match std::io::stdin().read(&mut byte) {
                Ok(0) => break,
                Ok(_) => continue,
                Err(_) => break,
            }
        }
        flag.store(false, Ordering::Release);
    });
    alive
}

#[cfg(test)]
#[cfg(unix)]
mod tests {
    use super::*;
    use ovrt_core::{OFFSET_ROUTE_BYTES, ROUTE_MAX_BYTES};

    fn buffer_with_route(route: &[u8]) -> Vec<u8> {
        let mut raw = vec![0_u8; BUFFER_TOTAL_BYTES as usize];
        let start = OFFSET_ROUTE_BYTES as usize;
        raw[start..start + route.len()].copy_from_slice(route);
        raw
    }

    #[test]
    fn a_route_is_read_back_without_its_padding() {
        let raw = buffer_with_route(b"pronto.fusion.v2");
        assert_eq!(read_route(&raw), Ok("pronto.fusion.v2"));
    }

    #[test]
    fn an_empty_route_is_refused_rather_than_dispatched() {
        // A zeroed route means the host published before writing the unit id.
        // Dispatching "" would look up a unit that does not exist and report it
        // as an unknown unit, hiding a protocol bug behind a routing error.
        let raw = vec![0_u8; BUFFER_TOTAL_BYTES as usize];
        assert!(read_route(&raw).is_err());
    }

    #[test]
    fn a_route_filling_the_whole_region_still_reads() {
        // No NUL terminator to find: the region length has to bound it.
        let raw = buffer_with_route(&vec![b'u'; ROUTE_MAX_BYTES as usize]);
        assert_eq!(read_route(&raw).map(str::len), Ok(ROUTE_MAX_BYTES as usize));
    }

    #[test]
    fn a_non_utf8_route_is_refused() {
        let raw = buffer_with_route(&[0xFF, 0xFE]);
        assert!(read_route(&raw).is_err());
    }
}
