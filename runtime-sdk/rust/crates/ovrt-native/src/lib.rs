#![forbid(unsafe_code)]

pub mod arena;
pub mod arena_blob;
mod buffer;
#[cfg(unix)]
pub mod epoch_transport;
mod shared_memory;
mod stdio;

use std::collections::BTreeMap;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::{Arc, Mutex, RwLock};
use std::thread;
use std::time::Duration;

use ovrt_core::{RuntimeDiagnostics, RuntimeMode, RuntimeRole};
use ovrt_unit::{RuntimeUnit, UnitRegistry};

#[cfg(unix)]
use ovrt_dispatch::{
    class_mask_for_role_index, decide as decide_from_table, DispatchRequest, SampledUnit,
};

pub use arena_blob::{ArenaBlobUnit, ARENA_BLOB_MAGIC, ARENA_BLOB_REQUEST_BYTES};
pub use buffer::NativeBuffer;
pub use shared_memory::serve_transport;
pub use stdio::{
    process_runtime_buffer, process_runtime_buffer_in_place, process_runtime_buffer_unpublished,
    serve_framed_session, serve_stdio, BufferOutcome,
};

type TaskResult = Result<Vec<u8>, String>;
const DEFAULT_DISPATCH_TIMEOUT: Duration = Duration::from_secs(30);

struct Task {
    unit_id: String,
    input: Vec<u8>,
    respond_to: Sender<TaskResult>,
}

pub struct NativeRuntimeHost {
    registry: UnitRegistry,
    diagnostics: Arc<RwLock<RuntimeDiagnostics>>,
    senders: BTreeMap<RuntimeRole, Sender<Task>>,
    in_flight: Arc<AtomicU32>,
    dispatch_timeout: Duration,
    /// Placement table consulted ahead of the static role pools.
    #[cfg(unix)]
    dispatch_block: Option<Arc<ovrt_dispatch::DispatchBlock>>,
    /// Local role pool to lane-slot mapping for placement and sampling.
    #[cfg(unix)]
    dispatch_lanes: BTreeMap<RuntimeRole, usize>,
    /// Jurisdiction this host serves; 0 accepts only global lanes, matching
    /// the fail-closed default for internal calls.
    #[cfg(unix)]
    dispatch_jurisdiction: u16,
}

impl NativeRuntimeHost {
    pub fn new(role_limits: BTreeMap<RuntimeRole, usize>) -> Self {
        let registry = UnitRegistry::default();
        let diagnostics = Arc::new(RwLock::new(RuntimeDiagnostics {
            mode: RuntimeMode::Native,
            ..RuntimeDiagnostics::default()
        }));
        let in_flight = Arc::new(AtomicU32::new(0));
        let mut senders = BTreeMap::new();
        let mut startup_errors = Vec::new();

        for (role, workers) in role_limits {
            let (sender, receiver) = mpsc::channel::<Task>();
            let shared_receiver = Arc::new(Mutex::new(receiver));
            let mut spawned_workers = 0;
            for worker_index in 0..workers.max(1) {
                let worker_registry = registry.clone();
                let worker_receiver = Arc::clone(&shared_receiver);
                let worker_diagnostics = Arc::clone(&diagnostics);
                let worker_in_flight = Arc::clone(&in_flight);
                match thread::Builder::new()
                    .name(format!("ovrt-native-{role}-{worker_index}"))
                    .spawn(move || {
                        worker_loop(
                            worker_registry,
                            worker_receiver,
                            worker_diagnostics,
                            worker_in_flight,
                        );
                    }) {
                    Ok(_) => {
                        spawned_workers += 1;
                    }
                    Err(error) => {
                        startup_errors.push(format!(
                            "spawn native runtime worker {role}/{worker_index}: {error}"
                        ));
                    }
                }
            }
            if spawned_workers > 0 {
                senders.insert(role, sender);
            } else {
                startup_errors.push(format!("native runtime role {role} has no workers"));
            }
        }

        if !startup_errors.is_empty() {
            if let Ok(mut guard) = diagnostics.write() {
                guard.degraded = true;
                guard.last_error = Some(startup_errors.join("; "));
                guard.last_runtime_source = "native-startup-error".to_string();
            }
        }

        Self {
            registry,
            diagnostics,
            senders,
            in_flight,
            dispatch_timeout: DEFAULT_DISPATCH_TIMEOUT,
            #[cfg(unix)]
            dispatch_block: None,
            #[cfg(unix)]
            dispatch_lanes: BTreeMap::new(),
            #[cfg(unix)]
            dispatch_jurisdiction: 0,
        }
    }

    pub fn with_dispatch_timeout(mut self, dispatch_timeout: Duration) -> Self {
        self.dispatch_timeout = dispatch_timeout;
        self
    }

    /// Attaches a placement table that every dispatch consults before falling
    /// back to the unit's default role pool.
    #[cfg(unix)]
    pub fn with_dispatch_block(
        mut self,
        block: Arc<ovrt_dispatch::DispatchBlock>,
        jurisdiction: u16,
    ) -> Self {
        self.dispatch_block = Some(block);
        self.dispatch_jurisdiction = jurisdiction;
        self
    }

    /// Maps one local role pool onto a lane slot of the placement table.
    ///
    /// Registered units for that role start sampling completions into the
    /// slot, and a winning selection on that slot routes work here.
    #[cfg(unix)]
    pub fn with_dispatch_lane(mut self, role: RuntimeRole, lane: usize) -> Self {
        self.dispatch_lanes.insert(role, lane);
        self
    }

    pub fn register_unit(&self, unit: Arc<dyn RuntimeUnit>) -> Result<(), String> {
        let descriptor = unit.descriptor();
        // When a lane slot is mapped for this role, completions sample into
        // it so placement decisions run on measured latency from here on.
        #[cfg(unix)]
        let unit: Arc<dyn RuntimeUnit> =
            match (&self.dispatch_block, self.dispatch_lanes.get(&descriptor.role)) {
                (Some(block), Some(lane)) => {
                    Arc::new(SampledUnit::new(unit, Arc::clone(block), *lane))
                }
                _ => unit,
            };
        self.registry.register(unit)?;
        let count = self.registry.descriptors()?.len() as u32;
        let mut guard = self
            .diagnostics
            .write()
            .map_err(|_| "runtime diagnostics lock poisoned".to_string())?;
        guard.active_units = count;
        Ok(())
    }

    pub fn dispatch(&self, unit_id: &str, input: Vec<u8>) -> Result<Vec<u8>, String> {
        let unit = self
            .registry
            .get(unit_id)?
            .ok_or_else(|| format!("runtime unit {unit_id} is not registered"))?;
        let descriptor = unit.descriptor();
        // Placement first: the table decides which mapped pool serves this
        // call; anything it cannot answer keeps today's default routing.
        #[cfg(unix)]
        let target_role = match &self.dispatch_block {
            Some(block) => self.select_role_by_placement(block, descriptor.role),
            None => descriptor.role,
        };
        #[cfg(not(unix))]
        let target_role = descriptor.role;
        let sender = self.senders.get(&target_role).ok_or_else(|| {
            format!("runtime role {target_role} does not have a native worker pool")
        })?;

        let in_flight = InFlightGuard::new(Arc::clone(&self.in_flight));
        let (respond_to, response) = mpsc::channel();
        sender
            .send(Task { unit_id: descriptor.unit_id, input, respond_to })
            .map_err(|_| "native runtime queue is unavailable".to_string())?;

        let result = match response.recv_timeout(self.dispatch_timeout) {
            Ok(result) => result,
            Err(mpsc::RecvTimeoutError::Timeout) => {
                let error =
                    format!("native runtime dispatch timed out after {:?}", self.dispatch_timeout);
                in_flight.finish();
                self.record_dispatch_failure("native-timeout", &error);
                return Err(error);
            }
            Err(mpsc::RecvTimeoutError::Disconnected) => {
                let error = "native runtime worker stopped unexpectedly".to_string();
                in_flight.finish();
                self.record_dispatch_failure("native-disconnected", &error);
                return Err(error);
            }
        };
        in_flight.finish();

        let mut guard = self
            .diagnostics
            .write()
            .map_err(|_| "runtime diagnostics lock poisoned".to_string())?;
        guard.in_flight = self.in_flight.load(Ordering::SeqCst);
        match &result {
            Ok(_) => {
                guard.last_error = None;
                guard.last_runtime_source = "native".to_string();
                guard.last_epoch = guard.last_epoch.saturating_add(1);
            }
            Err(error) => {
                guard.degraded = true;
                guard.last_error = Some(error.clone());
                guard.last_runtime_source = "native-error".to_string();
            }
        }

        result
    }

    pub fn dispatch_direct(&self, unit_id: &str, input: &[u8]) -> Result<Vec<u8>, String> {
        let unit = self
            .registry
            .get(unit_id)?
            .ok_or_else(|| format!("runtime unit {unit_id} is not registered"))?;
        let result = match catch_unwind(AssertUnwindSafe(|| unit.run(input))) {
            Ok(result) => result,
            Err(payload) => Err(panic_payload_message(payload)),
        };

        let mut guard = self
            .diagnostics
            .write()
            .map_err(|_| "runtime diagnostics lock poisoned".to_string())?;
        guard.in_flight = self.in_flight.load(Ordering::SeqCst);
        match &result {
            Ok(_) => {
                guard.last_error = None;
                guard.last_runtime_source = "native-ffi".to_string();
                guard.last_epoch = guard.last_epoch.saturating_add(1);
            }
            Err(error) => {
                guard.degraded = true;
                guard.last_error = Some(error.clone());
                guard.last_runtime_source = "native-ffi-error".to_string();
            }
        }

        result
    }

    pub fn diagnostics(&self) -> Result<RuntimeDiagnostics, String> {
        let guard =
            self.diagnostics.read().map_err(|_| "runtime diagnostics lock poisoned".to_string())?;
        Ok(guard.clone())
    }

    fn record_dispatch_failure(&self, source: &str, error: &str) {
        if let Ok(mut guard) = self.diagnostics.write() {
            guard.in_flight = self.in_flight.load(Ordering::SeqCst);
            guard.degraded = true;
            guard.last_error = Some(error.to_string());
            guard.last_runtime_source = source.to_string();
        }
    }

    /// Consults the placement table and returns the local pool that should
    /// serve this call.
    ///
    /// Every table-side failure degrades to the fallback role rather than
    /// failing the dispatch: a broken or unreadable table means dumber
    /// routing, never lost work.
    #[cfg(unix)]
    fn select_role_by_placement(
        &self,
        block: &ovrt_dispatch::DispatchBlock,
        fallback: RuntimeRole,
    ) -> RuntimeRole {
        // The click advances once per real decision so freshness windows run
        // on decisions, not wall time.
        let Ok(now) = block.advance_tick() else {
            return fallback;
        };
        let deadline_total = u128::from(self.dispatch_timeout.as_secs())
            .saturating_mul(1_000_000_000)
            .saturating_add(u128::from(self.dispatch_timeout.subsec_nanos()));
        let request = DispatchRequest {
            required_class_mask: class_mask_for_role_index(fallback as usize),
            jurisdiction: self.dispatch_jurisdiction,
            deadline_ns: if deadline_total > u128::from(u64::MAX) {
                u64::MAX
            } else {
                deadline_total as u64
            },
            affinity_key: 0,
        };

        let Ok(descriptors) = block.snapshot_descriptors() else {
            return fallback;
        };
        let stats: Vec<_> = (0..descriptors.len())
            .map(|lane| block.stat_row(lane).ok().map(|row| row.snapshot()))
            .collect();

        match decide_from_table(now, &descriptors, &stats, &request) {
            Some(lane_slot) => self
                .dispatch_lanes
                .iter()
                .find(|(_, slot)| **slot == usize::from(lane_slot))
                .map(|(role, _)| *role)
                .unwrap_or(fallback),
            None => fallback,
        }
    }
}

struct InFlightGuard {
    counter: Arc<AtomicU32>,
    active: bool,
}

impl InFlightGuard {
    fn new(counter: Arc<AtomicU32>) -> Self {
        counter.fetch_add(1, Ordering::SeqCst);
        Self { counter, active: true }
    }

    fn finish(mut self) {
        self.decrement();
    }

    fn decrement(&mut self) {
        if self.active {
            self.counter.fetch_sub(1, Ordering::SeqCst);
            self.active = false;
        }
    }
}

impl Drop for InFlightGuard {
    fn drop(&mut self) {
        self.decrement();
    }
}

fn worker_loop(
    registry: UnitRegistry,
    receiver: Arc<Mutex<Receiver<Task>>>,
    diagnostics: Arc<RwLock<RuntimeDiagnostics>>,
    in_flight: Arc<AtomicU32>,
) {
    loop {
        let task = {
            let guard = match receiver.lock() {
                Ok(guard) => guard,
                Err(_) => return,
            };
            match guard.recv() {
                Ok(task) => task,
                Err(_) => return,
            }
        };

        let result = match registry.get(&task.unit_id) {
            Ok(Some(unit)) => match catch_unwind(AssertUnwindSafe(|| unit.run(&task.input))) {
                Ok(result) => result,
                Err(payload) => Err(panic_payload_message(payload)),
            },
            Ok(None) => Err(format!("runtime unit {} is missing", task.unit_id)),
            Err(error) => Err(error),
        };

        if let Ok(mut guard) = diagnostics.write() {
            guard.in_flight = in_flight.load(Ordering::SeqCst);
            if let Err(error) = &result {
                guard.degraded = true;
                guard.last_error = Some(error.clone());
            }
        }

        let _ = task.respond_to.send(result);
    }
}

fn panic_payload_message(payload: Box<dyn std::any::Any + Send>) -> String {
    if let Some(message) = payload.downcast_ref::<&str>() {
        return format!("runtime unit panicked: {message}");
    }
    if let Some(message) = payload.downcast_ref::<String>() {
        return format!("runtime unit panicked: {message}");
    }
    "runtime unit panicked".to_string()
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;
    use std::io::Cursor;
    use std::sync::Arc;

    use ovrt_core::{
        RuntimeRole, RuntimeUnitDescriptor, BUFFER_TOTAL_BYTES, IDX_OUTPUT_WRITTEN,
        INT_IDX_STATUS_CODE,
    };
    use ovrt_unit::RuntimeUnit;

    use super::*;

    struct UppercaseUnit;

    impl RuntimeUnit for UppercaseUnit {
        fn descriptor(&self) -> RuntimeUnitDescriptor {
            RuntimeUnitDescriptor {
                unit_id: "text.compute".to_string(),
                role: RuntimeRole::Compute,
                input_schema: "foundation/v1/envelope.capnp".to_string(),
                output_schema: "foundation/v1/envelope.capnp".to_string(),
                supports_wasm: true,
                supports_native: true,
                requires_shared_memory: false,
                supports_gpu: false,
                max_concurrency: 2,
            }
        }

        fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
            Ok(input.iter().map(|byte| byte.to_ascii_uppercase()).collect())
        }
    }

    #[test]
    fn dispatches_registered_units_via_role_pool() {
        let mut role_limits = BTreeMap::new();
        role_limits.insert(RuntimeRole::Compute, 2);

        let host = NativeRuntimeHost::new(role_limits);
        host.register_unit(Arc::new(UppercaseUnit)).expect("register unit");

        let output = host.dispatch("text.compute", b"pulse".to_vec()).expect("dispatch unit");
        assert_eq!(output, b"PULSE");

        let diagnostics = host.diagnostics().expect("read diagnostics");
        assert_eq!(diagnostics.mode, RuntimeMode::Native);
        assert_eq!(diagnostics.active_units, 1);
    }

    #[test]
    fn dispatch_direct_uses_registered_units_without_role_pool() {
        let host = NativeRuntimeHost::new(BTreeMap::new());
        host.register_unit(Arc::new(UppercaseUnit)).expect("register unit");

        let output = host.dispatch_direct("text.compute", b"epochs").expect("dispatch direct");
        assert_eq!(output, b"EPOCHS");

        let diagnostics = host.diagnostics().expect("read diagnostics");
        assert_eq!(diagnostics.last_runtime_source, "native-ffi");
    }

    struct PanicUnit;

    impl RuntimeUnit for PanicUnit {
        fn descriptor(&self) -> RuntimeUnitDescriptor {
            RuntimeUnitDescriptor {
                unit_id: "panic.compute".to_string(),
                role: RuntimeRole::Compute,
                input_schema: "foundation/v1/envelope.capnp".to_string(),
                output_schema: "foundation/v1/envelope.capnp".to_string(),
                supports_wasm: false,
                supports_native: true,
                requires_shared_memory: false,
                supports_gpu: false,
                max_concurrency: 1,
            }
        }

        fn run(&self, _input: &[u8]) -> Result<Vec<u8>, String> {
            panic!("panic compute")
        }
    }

    #[test]
    fn converts_panics_into_runtime_errors() {
        let mut role_limits = BTreeMap::new();
        role_limits.insert(RuntimeRole::Compute, 1);

        let host = NativeRuntimeHost::new(role_limits);
        host.register_unit(Arc::new(PanicUnit)).expect("register unit");

        let err =
            host.dispatch("panic.compute", b"boom".to_vec()).expect_err("panic must be reported");
        assert!(err.contains("runtime unit panicked"));
    }

    struct SlowUnit;

    impl RuntimeUnit for SlowUnit {
        fn descriptor(&self) -> RuntimeUnitDescriptor {
            RuntimeUnitDescriptor {
                unit_id: "slow.compute".to_string(),
                role: RuntimeRole::Compute,
                input_schema: "foundation/v1/envelope.capnp".to_string(),
                output_schema: "foundation/v1/envelope.capnp".to_string(),
                supports_wasm: false,
                supports_native: true,
                requires_shared_memory: false,
                supports_gpu: false,
                max_concurrency: 1,
            }
        }

        fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
            std::thread::sleep(Duration::from_millis(25));
            Ok(input.to_vec())
        }
    }

    #[test]
    fn dispatch_times_out_instead_of_waiting_unbounded() {
        let mut role_limits = BTreeMap::new();
        role_limits.insert(RuntimeRole::Compute, 1);

        let host =
            NativeRuntimeHost::new(role_limits).with_dispatch_timeout(Duration::from_millis(1));
        host.register_unit(Arc::new(SlowUnit)).expect("register unit");

        let err = host
            .dispatch("slow.compute", b"late".to_vec())
            .expect_err("slow dispatch must time out");
        assert!(err.contains("timed out"));

        let diagnostics = host.diagnostics().expect("read diagnostics");
        assert!(diagnostics.degraded);
        assert_eq!(diagnostics.last_runtime_source, "native-timeout");
    }

    #[test]
    fn serves_runtime_buffers_over_framed_io() {
        let mut role_limits = BTreeMap::new();
        role_limits.insert(RuntimeRole::Compute, 1);

        let host = NativeRuntimeHost::new(role_limits);
        host.register_unit(Arc::new(UppercaseUnit)).expect("register unit");

        let mut buffer =
            NativeBuffer::new(vec![0_u8; BUFFER_TOTAL_BYTES as usize]).expect("buffer");
        buffer.initialize_control_plane(3).expect("init");
        buffer.write_input_bytes(b"pulse").expect("write input");
        let raw = buffer.into_inner();

        let mut input_stream = Vec::new();
        stdio::write_frame_for_test(&mut input_stream, b"text.compute").expect("write unit");
        stdio::write_frame_for_test(&mut input_stream, &raw).expect("write buffer");

        let mut reader = Cursor::new(input_stream);
        let mut writer = Cursor::new(Vec::<u8>::new());
        serve_framed_session(&host, &mut reader, &mut writer).expect("serve session");

        let output_frame = stdio::read_frame_for_test(&mut Cursor::new(writer.into_inner()))
            .expect("read output frame");
        let output_buffer = NativeBuffer::new(output_frame).expect("output buffer");
        assert_eq!(output_buffer.header_int(INT_IDX_STATUS_CODE).expect("status"), 0);
        assert_eq!(output_buffer.read_output_bytes().expect("output"), b"PULSE");
        assert_eq!(output_buffer.load_epoch(IDX_OUTPUT_WRITTEN), 1);
    }

    #[test]
    #[cfg(unix)]
    fn placement_table_routes_and_samples_completions() {
        use ovrt_core::DISPATCH_REGION_BYTES;
        use ovrt_dispatch::{class_mask_for_role_index, DispatchBlock, DispatchLaneDescriptor};

        struct EchoCompute;

        impl RuntimeUnit for EchoCompute {
            fn descriptor(&self) -> RuntimeUnitDescriptor {
                RuntimeUnitDescriptor {
                    unit_id: "echo.placement".to_string(),
                    role: RuntimeRole::Compute,
                    input_schema: "test".to_string(),
                    output_schema: "test".to_string(),
                    supports_wasm: true,
                    supports_native: true,
                    requires_shared_memory: false,
                    supports_gpu: false,
                    max_concurrency: 4,
                }
            }

            fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
                Ok(input.to_vec())
            }
        }

        let file = tempfile::NamedTempFile::new().expect("temp file");
        file.as_file().set_len(u64::from(DISPATCH_REGION_BYTES)).expect("size region");
        let block = Arc::new(DispatchBlock::open(file.path()).expect("open block"));

        let compute_bit = class_mask_for_role_index(RuntimeRole::Compute as usize);
        let lane0 = DispatchLaneDescriptor {
            lane_id: 0,
            jurisdiction: 0,
            max_concurrency: 4,
            generation: 1,
            unit_class_mask: compute_bit,
            affinity_bloom: 0,
        };
        block.publish_descriptors(&[lane0], 1).expect("publish table");

        let mut role_limits = BTreeMap::new();
        role_limits.insert(RuntimeRole::Compute, 2);
        let host = NativeRuntimeHost::new(role_limits)
            .with_dispatch_block(Arc::clone(&block), 0)
            .with_dispatch_lane(RuntimeRole::Compute, 0);
        host.register_unit(Arc::new(EchoCompute)).expect("register unit");

        let output = host.dispatch("echo.placement", b"ping".to_vec()).expect("dispatch");
        assert_eq!(output, b"ping");

        // The routed completion must have sampled into the mapped lane and
        // left the in-flight counter balanced.
        let stats = block.stat_row(0).expect("stat row").snapshot();
        assert!(stats.ewma_ns > 0, "completion must seed the measured estimate");
        assert_eq!(stats.inflight, 0, "claim/release must stay balanced");
        assert!(stats.last_tick_seen > 0, "heartbeat must carry a real click");
    }
}
