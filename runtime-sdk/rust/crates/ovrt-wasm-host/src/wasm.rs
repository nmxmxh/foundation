//! WASM lane: instantiate a guest artifact and execute one exchange.
//!
//! The import surface mirrors `BrowserRuntimeHost.getImportObject` and the
//! `ovrt_browser::js_interop` extern block, so a guest built for the browser
//! runs here unchanged. Bounds are engine-enforced: fuel stops runaway
//! compute, a resource limiter caps linear memory, and an epoch watchdog
//! stops exchanges that outlive their deadline.

#![forbid(unsafe_code)]

use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use ovrt_native::NativeBuffer;
use wasmtime::{
    Caller, Config, Engine, Error, Linker, Memory, Module, ResourceLimiter, Store, TypedFunc,
};

use crate::limits::ResourceLimits;
use crate::native::build_initial_buffer;
use crate::{snapshot::BufferSnapshot, DEFAULT_PINNED_NOW_MS};

/// Required guest export that performs one exchange.
pub const GUEST_ENTRY: &str = "ovrt_unit_run";
/// Handle assigned to the control buffer inside the guest handle table.
pub const CONTROL_BUFFER_HANDLE: u32 = 1;
const LOG_CAP: usize = 64;
const WATCHDOG_POLL: Duration = Duration::from_millis(5);

/// Result of one guest exchange beyond the buffer state itself.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WasmOutcome {
    pub snapshot: BufferSnapshot,
    pub guest_status: i32,
    pub fuel_consumed: u64,
}

struct MemoryLimiter {
    ceiling_bytes: u64,
}

impl MemoryLimiter {
    #[cfg(test)]
    fn new(ceiling_bytes: u64) -> Self {
        Self { ceiling_bytes }
    }
}

impl ResourceLimiter for MemoryLimiter {
    fn memory_growing(
        &mut self,
        _current: usize,
        desired: usize,
        _maximum: Option<usize>,
    ) -> Result<bool, Error> {
        Ok(desired as u64 <= self.ceiling_bytes)
    }

    fn table_growing(
        &mut self,
        _current: usize,
        desired: usize,
        _maximum: Option<usize>,
    ) -> Result<bool, Error> {
        // Tables hold references, not payload bytes; a generous fixed bound
        // keeps hostile guests bounded without constraining honest ones.
        Ok(desired <= 10_000)
    }
}

struct GuestState {
    buffers: HashMap<u32, Vec<u8>>,
    memory: Option<Memory>,
    logs: Vec<(u8, String)>,
    limiter: MemoryLimiter,
    env_now_ms: f64,
}

impl GuestState {
    fn new(limits: &ResourceLimits, env_now_ms: f64) -> Self {
        Self {
            buffers: HashMap::new(),
            memory: None,
            logs: Vec::new(),
            limiter: MemoryLimiter { ceiling_bytes: limits.memory_ceiling_bytes() },
            env_now_ms,
        }
    }

    fn buffer(&self, handle: u32) -> Result<&Vec<u8>, Error> {
        self.buffers
            .get(&handle)
            .ok_or_else(|| Error::msg(format!("unknown runtime buffer handle {handle}")))
    }

    fn buffer_mut(&mut self, handle: u32) -> Result<&mut Vec<u8>, Error> {
        self.buffers
            .get_mut(&handle)
            .ok_or_else(|| Error::msg(format!("unknown runtime buffer handle {handle}")))
    }
}

/// A compiled guest kept warm across exchanges.
///
/// Instantiation dominates a one-shot exchange by orders of magnitude, so
/// callers measuring or exercising steady-state behaviour should compile
/// once and [`Self::exchange`] repeatedly. Each exchange resets the control
/// buffer, refuels the store, and re-arms the deadline watchdog, so outcomes
/// are independent of history.
pub struct WasmGuest {
    engine: Engine,
    store: Store<GuestState>,
    run: TypedFunc<u32, i32>,
    max_fuel: u64,
    timeout_ms: u64,
    max_input_bytes: u32,
}

impl WasmGuest {
    /// Compiles `wasm_artifact` and instantiates it under the given budgets.
    pub fn compile(
        wasm_artifact: &[u8],
        limits: &ResourceLimits,
        env_now_ms: f64,
    ) -> Result<Self, String> {
        limits.validate()?;
        let engine = build_engine()?;
        let module = Module::new(&engine, wasm_artifact)
            .map_err(|error| format!("invalid wasm artifact: {error}"))?;
        let mut state = GuestState::new(limits, env_now_ms);
        state.buffers.insert(CONTROL_BUFFER_HANDLE, vec![0u8; BUFFER_TOTAL_BYTES_USIZE]);
        let mut store = Store::new(&engine, state);
        store.limiter(|guest| &mut guest.limiter);
        store.epoch_deadline_trap();

        let mut linker = Linker::new(&engine);
        define_imports(&mut linker);
        let instance = linker
            .instantiate(&mut store, &module)
            .map_err(|error| format!("guest instantiation failed: {error}"))?;
        let memory = instance
            .get_memory(&mut store, "memory")
            .ok_or_else(|| "guest does not export memory".to_string())?;
        store.data_mut().memory = Some(memory);
        let run: TypedFunc<u32, i32> = instance
            .get_typed_func::<u32, i32>(&mut store, GUEST_ENTRY)
            .map_err(|_| format!("guest does not export {GUEST_ENTRY}(u32) -> i32"))?;

        Ok(Self {
            engine,
            store,
            run,
            max_fuel: limits.max_fuel,
            timeout_ms: limits.timeout_ms,
            max_input_bytes: limits.max_input_bytes,
        })
    }

    /// Runs one exchange against a freshly initialized control buffer.
    pub fn exchange(&mut self, input: &[u8], module_version: i32) -> Result<WasmOutcome, String> {
        if u32::try_from(input.len()).unwrap_or(u32::MAX) > self.max_input_bytes {
            return Err(format!(
                "input payload exceeds the declared budget: {} > {}",
                input.len(),
                self.max_input_bytes
            ));
        }
        let initial = build_initial_buffer(input, module_version)?;
        self.store.data_mut().buffers.insert(CONTROL_BUFFER_HANDLE, initial.into_inner());
        self.store
            .set_fuel(self.max_fuel)
            .map_err(|error| format!("fuel setup failed: {error}"))?;
        self.store.set_epoch_deadline(1);

        let watchdog = DeadlineWatchdog::start(self.engine.clone(), self.timeout_ms);
        let called = self.run.call(&mut self.store, CONTROL_BUFFER_HANDLE);
        watchdog.stop();
        let guest_status = called.map_err(map_guest_trap)?;

        let raw = self
            .store
            .data()
            .buffer(CONTROL_BUFFER_HANDLE)
            .map_err(|error| error.to_string())?
            .clone();
        let snapshot =
            BufferSnapshot::capture(&NativeBuffer::new(raw)?).map_err(|error| error.to_string())?;
        let remaining =
            self.store.get_fuel().map_err(|error| format!("fuel read failed: {error}"))?;
        Ok(WasmOutcome { snapshot, guest_status, fuel_consumed: self.max_fuel - remaining })
    }
}

const BUFFER_TOTAL_BYTES_USIZE: usize = ovrt_core::BUFFER_TOTAL_BYTES as usize;

/// Executes one exchange of a compiled guest against the control-buffer contract.
pub fn execute(
    wasm_artifact: &[u8],
    input: &[u8],
    limits: &ResourceLimits,
    module_version: i32,
) -> Result<WasmOutcome, String> {
    execute_with_env(wasm_artifact, input, limits, module_version, DEFAULT_PINNED_NOW_MS)
}

/// Executes one exchange with an explicit clock value for `ovrt_get_now`.
///
/// Pinning the clock makes time-reading guests deterministic, which is what
/// lets their native and WASM lanes agree byte-for-byte. The native-side unit
/// must observe the same value through its own injected clock. This is the
/// one-shot convenience path; steady-state callers should keep a
/// [`WasmGuest`] alive instead of paying instantiation per exchange.
pub fn execute_with_env(
    wasm_artifact: &[u8],
    input: &[u8],
    limits: &ResourceLimits,
    module_version: i32,
    env_now_ms: f64,
) -> Result<WasmOutcome, String> {
    let mut guest = WasmGuest::compile(wasm_artifact, limits, env_now_ms)?;
    guest.exchange(input, module_version)
}

fn build_engine() -> Result<Engine, String> {
    let mut config = Config::new();
    config.consume_fuel(true);
    config.epoch_interruption(true);
    Engine::new(&config).map_err(|error| format!("engine startup failed: {error}"))
}

fn map_guest_trap(error: Error) -> String {
    // Debug formatting includes the full cause chain, which plain Display
    // truncates to the wasm backtrace alone.
    let text = format!("{error:?}");
    if text.contains("fuel") {
        return format!("guest exceeded its fuel budget: {text}");
    }
    if text.contains("epoch") || text.contains("interrupt") {
        return format!("guest exceeded its time budget: {text}");
    }
    format!("guest trapped: {text}")
}

/// Increments the engine epoch once the deadline passes, trapping the guest.
struct DeadlineWatchdog {
    cancel: Arc<AtomicBool>,
    handle: Option<JoinHandle<()>>,
}

impl DeadlineWatchdog {
    fn start(engine: Engine, timeout_ms: u64) -> Self {
        let cancel = Arc::new(AtomicBool::new(false));
        let flag = Arc::clone(&cancel);
        let handle = thread::spawn(move || {
            // Bounded poll: at most timeout/POLL iterations, each sleep capped
            // by the remaining budget.
            let deadline = Instant::now() + Duration::from_millis(timeout_ms);
            while !flag.load(Ordering::Acquire) {
                let now = Instant::now();
                if now >= deadline {
                    break;
                }
                thread::sleep(WATCHDOG_POLL.min(deadline - now));
            }
            if !flag.load(Ordering::Acquire) {
                engine.increment_epoch();
            }
        });
        Self { cancel, handle: Some(handle) }
    }

    fn stop(mut self) {
        self.cancel.store(true, Ordering::Release);
        if let Some(handle) = self.handle.take() {
            let _ = handle.join();
        }
    }
}

fn guest_memory(caller: &Caller<'_, GuestState>) -> Result<Memory, Error> {
    caller.data().memory.ok_or_else(|| Error::msg("guest memory is not attached"))
}

fn define_imports(linker: &mut Linker<GuestState>) {
    linker
        .func_wrap("env", "ovrt_get_byte_length", |state: Caller<'_, GuestState>, handle: u32| {
            state.data().buffer(handle).map(|buffer| buffer.len() as u32)
        })
        .expect("import definition is static and cannot fail");
    linker
        .func_wrap(
            "env",
            "ovrt_copy_to_buffer",
            |mut caller: Caller<'_, GuestState>,
             handle: u32,
             target: u32,
             source: u32,
             len: u32| { copy_to_buffer(&mut caller, handle, target, source, len) },
        )
        .expect("import definition is static and cannot fail");
    linker
        .func_wrap(
            "env",
            "ovrt_copy_from_buffer",
            |mut caller: Caller<'_, GuestState>,
             handle: u32,
             source: u32,
             target: u32,
             len: u32| { copy_from_buffer(&mut caller, handle, source, target, len) },
        )
        .expect("import definition is static and cannot fail");
    define_atomic_imports(linker);
    define_env_imports(linker);
}

fn copy_to_buffer(
    caller: &mut Caller<'_, GuestState>,
    handle: u32,
    target: u32,
    source: u32,
    len: u32,
) -> Result<(), Error> {
    let memory = guest_memory(caller)?;
    let mut bytes = vec![0u8; len as usize];
    memory
        .read(&*caller, source as usize, &mut bytes)
        .map_err(|error| Error::msg(error.to_string()))?;
    let buffer = caller.data_mut().buffer_mut(handle)?;
    let end = target as usize + bytes.len();
    if end > buffer.len() {
        return Err(Error::msg("copy target exceeds buffer bounds"));
    }
    buffer[target as usize..end].copy_from_slice(&bytes);
    Ok(())
}

fn copy_from_buffer(
    caller: &mut Caller<'_, GuestState>,
    handle: u32,
    source: u32,
    target: u32,
    len: u32,
) -> Result<(), Error> {
    let memory = guest_memory(caller)?;
    let buffer = caller.data().buffer(handle)?;
    let end = source as usize + len as usize;
    if end > buffer.len() {
        return Err(Error::msg("copy source exceeds buffer bounds"));
    }
    let bytes = buffer[source as usize..end].to_vec();
    memory.write(caller, target as usize, &bytes).map_err(|error| Error::msg(error.to_string()))
}

fn define_atomic_imports(linker: &mut Linker<GuestState>) {
    linker
        .func_wrap(
            "env",
            "ovrt_atomic_load",
            |state: Caller<'_, GuestState>, handle: u32, index: u32| {
                atomic_read(state, handle, index)
            },
        )
        .expect("import definition is static and cannot fail");
    linker
        .func_wrap(
            "env",
            "ovrt_atomic_store",
            |state: Caller<'_, GuestState>, handle: u32, index: u32, value: i32| {
                atomic_write(state, handle, index, |slot| {
                    let old = *slot;
                    *slot = value;
                    old
                })
            },
        )
        .expect("import definition is static and cannot fail");
    linker
        .func_wrap(
            "env",
            "ovrt_atomic_add",
            |state: Caller<'_, GuestState>, handle: u32, index: u32, delta: i32| {
                atomic_write(state, handle, index, |slot| {
                    let old = *slot;
                    *slot = old.wrapping_add(delta);
                    old
                })
            },
        )
        .expect("import definition is static and cannot fail");
    linker
        .func_wrap(
            "env",
            "ovrt_atomic_compare_exchange",
            |state: Caller<'_, GuestState>,
             handle: u32,
             index: u32,
             expected: i32,
             replacement: i32| {
                atomic_write(state, handle, index, |slot| {
                    let old = *slot;
                    if old == expected {
                        *slot = replacement;
                    }
                    old
                })
            },
        )
        .expect("import definition is static and cannot fail");
    linker
        .func_wrap(
            "env",
            "ovrt_atomic_notify",
            |_state: Caller<'_, GuestState>, _handle: u32, _index: u32, _count: i32| 0i32,
        )
        .expect("import definition is static and cannot fail");
}

fn slot_range(index: u32) -> Result<(u32, u32), Error> {
    let start = index.checked_mul(4).ok_or_else(|| Error::msg("atomic byte offset overflows"))?;
    Ok((start, start + 4))
}

fn atomic_read_slot(state: &GuestState, handle: u32, index: u32) -> Result<i32, Error> {
    let (start, end) = slot_range(index)?;
    let buffer = state.buffer(handle)?;
    let slice = buffer
        .get(start as usize..end as usize)
        .ok_or_else(|| Error::msg("atomic index out of bounds"))?;
    let bytes: [u8; 4] =
        slice.try_into().map_err(|_| Error::msg("atomic slot must be four bytes"))?;
    Ok(i32::from_le_bytes(bytes))
}

fn atomic_write_slot(
    state: &mut GuestState,
    handle: u32,
    index: u32,
    apply: impl FnOnce(&mut i32) -> i32,
) -> Result<i32, Error> {
    let (start, end) = slot_range(index)?;
    let buffer = state.buffer_mut(handle)?;
    let slice = buffer
        .get_mut(start as usize..end as usize)
        .ok_or_else(|| Error::msg("atomic index out of bounds"))?;
    let bytes: [u8; 4] =
        slice.try_into().map_err(|_| Error::msg("atomic slot must be four bytes"))?;
    let mut value = i32::from_le_bytes(bytes);
    let old = apply(&mut value);
    slice.copy_from_slice(&value.to_le_bytes());
    Ok(old)
}

fn atomic_read(state: Caller<'_, GuestState>, handle: u32, index: u32) -> Result<i32, Error> {
    atomic_read_slot(state.data(), handle, index)
}

fn atomic_write(
    mut state: Caller<'_, GuestState>,
    handle: u32,
    index: u32,
    apply: impl FnOnce(&mut i32) -> i32,
) -> Result<i32, Error> {
    atomic_write_slot(state.data_mut(), handle, index, apply)
}

fn define_env_imports(linker: &mut Linker<GuestState>) {
    linker
        .func_wrap(
            "env",
            "ovrt_log",
            |mut state: Caller<'_, GuestState>, pointer: u32, len: u32, level: u32| {
                log_message(&mut state, pointer, len, level)
            },
        )
        .expect("import definition is static and cannot fail");
    linker
        .func_wrap("env", "ovrt_log_ring", |_state: Caller<'_, GuestState>, _p: u32, _l: u32| ())
        .expect("import definition is static and cannot fail");
    linker
        .func_wrap("env", "ovrt_get_now", |state: Caller<'_, GuestState>| state.data().env_now_ms)
        .expect("import definition is static and cannot fail");
    linker
        .func_wrap(
            "env",
            "ovrt_fill_random",
            |mut caller: Caller<'_, GuestState>, pointer: u32, len: u32| {
                fill_deterministic(&mut caller, pointer, len)
            },
        )
        .expect("import definition is static and cannot fail");
}

fn log_message(
    caller: &mut Caller<'_, GuestState>,
    pointer: u32,
    len: u32,
    level: u32,
) -> Result<(), Error> {
    let memory = guest_memory(caller)?;
    let mut bytes = vec![0u8; len as usize];
    memory
        .read(&*caller, pointer as usize, &mut bytes)
        .map_err(|error| Error::msg(error.to_string()))?;
    let message = String::from_utf8_lossy(&bytes).into_owned();
    let logs = &mut caller.data_mut().logs;
    if logs.len() < LOG_CAP {
        logs.push((level.min(u8::MAX as u32) as u8, message));
    }
    Ok(())
}

fn fill_deterministic(
    caller: &mut Caller<'_, GuestState>,
    pointer: u32,
    len: u32,
) -> Result<(), Error> {
    // Mirrors the ovrt-browser native fallback so both lanes stay repeatable.
    let pattern: Vec<u8> = (0..len).map(|index| (index % 255) as u8).collect();
    let memory = guest_memory(caller)?;
    memory.write(caller, pointer as usize, &pattern).map_err(|error| Error::msg(error.to_string()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::limits::WASM_PAGE_BYTES;

    fn state_with_buffer() -> GuestState {
        let mut state = GuestState::new(&ResourceLimits::for_compute(), DEFAULT_PINNED_NOW_MS);
        state.buffers.insert(CONTROL_BUFFER_HANDLE, vec![0u8; 4096]);
        state
    }

    #[test]
    fn pinned_clock_is_reported_to_guests() {
        let state = GuestState::new(&ResourceLimits::for_compute(), 1234.5);
        assert_eq!(state.env_now_ms, 1234.5);
    }

    #[test]
    fn limiter_rejects_growth_beyond_the_ceiling() {
        let mut limiter = MemoryLimiter::new(2 * WASM_PAGE_BYTES);
        assert!(limiter.memory_growing(0, WASM_PAGE_BYTES as usize, None).expect("admits"));
        assert!(limiter.memory_growing(1, 2, None).expect("admits"));
        assert!(!limiter.memory_growing(0, 3 * WASM_PAGE_BYTES as usize, None).expect("rejects"));
    }

    #[test]
    fn unknown_handles_are_trapped_not_panicked() {
        let state = state_with_buffer();
        let error = state.buffer(99).expect_err("unknown handle must trap");
        assert!(error.to_string().contains("unknown runtime buffer handle"));
    }

    #[test]
    fn atomic_slots_read_and_write_little_endian_u32() {
        let mut state = state_with_buffer();
        let old = atomic_write_slot(&mut state, CONTROL_BUFFER_HANDLE, 2, |slot| {
            *slot = 7;
            let prior = *slot;
            *slot = prior + 1;
            prior
        })
        .expect("write");
        assert_eq!(old, 7);
        assert_eq!(atomic_read_slot(&state, CONTROL_BUFFER_HANDLE, 2).expect("read"), 8);
    }

    #[test]
    fn compare_exchange_only_moves_on_match() {
        let mut state = state_with_buffer();
        let _ = atomic_write_slot(&mut state, CONTROL_BUFFER_HANDLE, 3, |slot| {
            *slot = 5;
            *slot
        });
        let cas = |state: &mut GuestState, expected: i32| {
            atomic_write_slot(state, CONTROL_BUFFER_HANDLE, 3, |slot| {
                let old = *slot;
                if old == expected {
                    *slot = 9;
                }
                old
            })
            .expect("cas")
        };
        assert_eq!(cas(&mut state, 4), 5, "mismatch leaves the slot untouched");
        assert_eq!(atomic_read_slot(&state, CONTROL_BUFFER_HANDLE, 3).expect("read"), 5);
        assert_eq!(cas(&mut state, 5), 5, "match applies the replacement");
        assert_eq!(atomic_read_slot(&state, CONTROL_BUFFER_HANDLE, 3).expect("read"), 9);
    }

    #[test]
    fn out_of_bounds_atomic_indexes_trap() {
        let state = state_with_buffer();
        assert!(atomic_read_slot(&state, CONTROL_BUFFER_HANDLE, 4096).is_err());
        let mut state = state_with_buffer();
        assert!(
            atomic_write_slot(&mut state, CONTROL_BUFFER_HANDLE, u32::MAX, |slot| *slot).is_err()
        );
    }

    #[test]
    fn watchdog_stops_without_firing_when_cancelled() {
        let engine = build_engine().expect("engine");
        let watchdog = DeadlineWatchdog::start(engine.clone(), 60_000);
        watchdog.stop();
        // The engine stays usable and manually incrementable after a clean stop.
        engine.increment_epoch();
    }
}
