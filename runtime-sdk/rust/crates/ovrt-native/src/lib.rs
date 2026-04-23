#![forbid(unsafe_code)]

mod buffer;
mod shared_memory;
mod stdio;

use std::collections::BTreeMap;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::{Arc, Mutex, RwLock};
use std::thread;

use ovrt_core::{RuntimeDiagnostics, RuntimeMode, RuntimeRole};
use ovrt_unit::{RuntimeUnit, UnitRegistry};

pub use buffer::NativeBuffer;
pub use shared_memory::serve_transport;
pub use stdio::{
    process_runtime_buffer, process_runtime_buffer_in_place, serve_framed_session, serve_stdio,
};

type TaskResult = Result<Vec<u8>, String>;

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

        for (role, workers) in role_limits {
            let (sender, receiver) = mpsc::channel::<Task>();
            let shared_receiver = Arc::new(Mutex::new(receiver));
            for worker_index in 0..workers.max(1) {
                let worker_registry = registry.clone();
                let worker_receiver = Arc::clone(&shared_receiver);
                let worker_diagnostics = Arc::clone(&diagnostics);
                let worker_in_flight = Arc::clone(&in_flight);
                let _ = thread::Builder::new()
                    .name(format!("ovrt-native-{role}-{worker_index}"))
                    .spawn(move || {
                        worker_loop(
                            worker_registry,
                            worker_receiver,
                            worker_diagnostics,
                            worker_in_flight,
                        );
                    });
            }
            senders.insert(role, sender);
        }

        Self { registry, diagnostics, senders, in_flight }
    }

    pub fn register_unit(&self, unit: Arc<dyn RuntimeUnit>) -> Result<(), String> {
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
        let sender = self.senders.get(&descriptor.role).ok_or_else(|| {
            format!("runtime role {} does not have a native worker pool", descriptor.role)
        })?;

        self.in_flight.fetch_add(1, Ordering::SeqCst);
        let (respond_to, response) = mpsc::channel();
        sender.send(Task { unit_id: descriptor.unit_id, input, respond_to }).map_err(|_| {
            self.in_flight.fetch_sub(1, Ordering::SeqCst);
            "native runtime queue is unavailable".to_string()
        })?;

        let result = response.recv().map_err(|_| {
            self.in_flight.fetch_sub(1, Ordering::SeqCst);
            "native runtime worker stopped unexpectedly".to_string()
        })?;
        self.in_flight.fetch_sub(1, Ordering::SeqCst);

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

        let _ = task.respond_to.send(result.clone());

        if let Ok(mut guard) = diagnostics.write() {
            guard.in_flight = in_flight.load(Ordering::SeqCst);
            if let Err(error) = result {
                guard.degraded = true;
                guard.last_error = Some(error);
            }
        }
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
                input_schema: "common/v1/envelope.capnp".to_string(),
                output_schema: "common/v1/envelope.capnp".to_string(),
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
                input_schema: "common/v1/envelope.capnp".to_string(),
                output_schema: "common/v1/envelope.capnp".to_string(),
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
}
