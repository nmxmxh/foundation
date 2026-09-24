#![forbid(unsafe_code)]

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use arc_swap::ArcSwap;

use ovrt_core::RuntimeUnitDescriptor;

mod output;
pub use output::{RuntimeOutput, SliceOutput};

pub trait RuntimeUnit: Send + Sync {
    fn descriptor(&self) -> RuntimeUnitDescriptor;
    fn execute(&self, input: &[u8], output: &mut dyn RuntimeOutput) -> Result<(), String>;

    /// Returns owned output when the caller has no existing destination.
    fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
        let mut output = Vec::new();
        self.execute(input, &mut output)?;
        Ok(output)
    }
}

#[derive(Default, Clone)]
pub struct UnitRegistry {
    units: Arc<ArcSwap<BTreeMap<String, Arc<RegisteredUnit>>>>,
    writer: Arc<Mutex<()>>,
}

/// Registration owns validated metadata and pins the implementation used by a queued request.
pub struct RegisteredUnit {
    descriptor: RuntimeUnitDescriptor,
    unit: Arc<dyn RuntimeUnit>,
}

impl RegisteredUnit {
    pub fn descriptor(&self) -> &RuntimeUnitDescriptor {
        &self.descriptor
    }

    pub fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
        self.unit.run(input)
    }

    pub fn execute(&self, input: &[u8], output: &mut dyn RuntimeOutput) -> Result<(), String> {
        self.unit.execute(input, output)
    }
}

impl UnitRegistry {
    pub fn register(&self, unit: Arc<dyn RuntimeUnit>) -> Result<(), String> {
        let descriptor = unit.descriptor();
        descriptor.validate()?;

        let _writer =
            self.writer.lock().map_err(|_| "runtime unit registry lock poisoned".to_string())?;
        let mut next = (**self.units.load()).clone();
        next.insert(descriptor.unit_id.clone(), Arc::new(RegisteredUnit { descriptor, unit }));
        self.units.store(Arc::new(next));
        Ok(())
    }

    pub fn get(&self, unit_id: &str) -> Result<Option<Arc<dyn RuntimeUnit>>, String> {
        let guard = self.units.load();
        Ok(guard.get(unit_id).map(|entry| Arc::clone(&entry.unit)))
    }

    pub fn resolve(&self, unit_id: &str) -> Result<Option<Arc<RegisteredUnit>>, String> {
        let guard = self.units.load();
        Ok(guard.get(unit_id).cloned())
    }

    pub fn len(&self) -> Result<usize, String> {
        let guard = self.units.load();
        Ok(guard.len())
    }

    pub fn is_empty(&self) -> Result<bool, String> {
        self.len().map(|count| count == 0)
    }

    pub fn descriptors(&self) -> Result<Vec<RuntimeUnitDescriptor>, String> {
        let guard = self.units.load();
        Ok(guard.values().map(|entry| entry.descriptor.clone()).collect())
    }

    /// Pins a registry generation without a read lock or a unit reference-count update.
    pub fn with_unit<T>(
        &self,
        unit_id: &str,
        operation: impl FnOnce(&RegisteredUnit) -> T,
    ) -> Option<T> {
        let snapshot = self.units.load();
        snapshot.get(unit_id).map(|unit| operation(unit))
    }
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor};

    use super::*;

    struct EchoUnit;

    impl RuntimeUnit for EchoUnit {
        fn descriptor(&self) -> RuntimeUnitDescriptor {
            RuntimeUnitDescriptor {
                unit_id: "echo.compute".to_string(),
                role: RuntimeRole::Compute,
                input_schema: "foundation/v1/envelope.capnp".to_string(),
                output_schema: "foundation/v1/envelope.capnp".to_string(),
                supports_wasm: true,
                supports_native: true,
                requires_shared_memory: false,
                supports_gpu: false,
                max_concurrency: 1,
            }
        }

        fn execute(
            &self,
            input: &[u8],
            __ovrt_output: &mut dyn RuntimeOutput,
        ) -> Result<(), String> {
            __ovrt_output.write(input)
        }
    }

    #[test]
    fn registers_and_reads_units() {
        let registry = UnitRegistry::default();
        registry.register(Arc::new(EchoUnit)).expect("register unit");

        let unit = registry.get("echo.compute").expect("registry access").expect("unit must exist");
        assert_eq!(unit.run(b"ping").expect("run unit"), b"ping");
    }

    struct CountedUnit {
        calls: Arc<std::sync::atomic::AtomicUsize>,
        output: &'static [u8],
    }

    impl RuntimeUnit for CountedUnit {
        fn descriptor(&self) -> RuntimeUnitDescriptor {
            self.calls.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
            EchoUnit.descriptor()
        }

        fn execute(&self, _: &[u8], __ovrt_output: &mut dyn RuntimeOutput) -> Result<(), String> {
            __ovrt_output.write(self.output)
        }
    }

    #[test]
    fn metadata_is_evaluated_once_and_resolved_units_survive_replacement() {
        use std::sync::atomic::{AtomicUsize, Ordering};
        let registry = UnitRegistry::default();
        let calls = Arc::new(AtomicUsize::new(0));
        assert!(registry.is_empty().expect("empty"));
        registry
            .register(Arc::new(CountedUnit { calls: Arc::clone(&calls), output: b"old" }))
            .expect("register");
        let old = registry.resolve("echo.compute").expect("resolve").expect("unit");
        assert_eq!(old.descriptor().role, RuntimeRole::Compute);
        assert_eq!(registry.descriptors().expect("descriptors").len(), 1);
        assert_eq!(registry.len().expect("length"), 1);
        assert_eq!(calls.load(Ordering::Relaxed), 1);
        registry.register(Arc::new(CountedUnit { calls, output: b"new" })).expect("replace");
        assert_eq!(old.run(b"").expect("old unit"), b"old");
        assert_eq!(
            registry
                .resolve("echo.compute")
                .expect("resolve")
                .expect("unit")
                .run(b"")
                .expect("new unit"),
            b"new"
        );
        assert!(registry.resolve("missing").expect("missing").is_none());
        assert_eq!(registry.len().expect("replacement length"), 1);
    }

    #[test]
    fn a_pinned_generation_allows_reentrant_replacement() {
        let registry = UnitRegistry::default();
        let calls = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        registry
            .register(Arc::new(CountedUnit { calls: Arc::clone(&calls), output: b"old" }))
            .expect("initial");
        registry
            .with_unit("echo.compute", |unit| {
                registry
                    .register(Arc::new(CountedUnit { calls, output: b"new" }))
                    .expect("replace during call");
                assert_eq!(unit.run(b"").expect("pinned result"), b"old");
            })
            .expect("old generation");
        assert_eq!(
            registry
                .with_unit("echo.compute", |unit| unit.run(b""))
                .expect("new generation")
                .expect("result"),
            b"new"
        );
        assert!(registry.with_unit("missing", |_| ()).is_none());
    }

    #[test]
    fn concurrent_registration_preserves_every_generation_update() {
        struct NamedUnit(String);
        impl RuntimeUnit for NamedUnit {
            fn descriptor(&self) -> RuntimeUnitDescriptor {
                let mut descriptor = EchoUnit.descriptor();
                descriptor.unit_id.clone_from(&self.0);
                descriptor
            }

            fn execute(&self, input: &[u8], output: &mut dyn RuntimeOutput) -> Result<(), String> {
                output.write(input)
            }
        }
        let registry = UnitRegistry::default();
        std::thread::scope(|scope| {
            for worker in 0..8 {
                let registry = &registry;
                scope.spawn(move || {
                    for index in 0..16 {
                        let id = format!("unit.{worker}.{index}");
                        registry.register(Arc::new(NamedUnit(id.clone()))).expect("register");
                        registry
                            .with_unit(&id, |unit| {
                                assert_eq!(unit.run(b"result").expect("result"), b"result");
                            })
                            .expect("visible generation");
                    }
                });
            }
        });
        assert_eq!(registry.len().expect("all registrations"), 128);
    }
}
