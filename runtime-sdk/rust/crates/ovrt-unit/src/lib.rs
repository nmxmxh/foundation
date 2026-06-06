#![forbid(unsafe_code)]

use std::collections::BTreeMap;
use std::sync::{Arc, RwLock};

use ovrt_core::RuntimeUnitDescriptor;

pub trait RuntimeUnit: Send + Sync {
    fn descriptor(&self) -> RuntimeUnitDescriptor;
    fn run(&self, input: &[u8]) -> Result<Vec<u8>, String>;
}

#[derive(Default, Clone)]
pub struct UnitRegistry {
    units: Arc<RwLock<BTreeMap<String, Arc<dyn RuntimeUnit>>>>,
}

impl UnitRegistry {
    pub fn register(&self, unit: Arc<dyn RuntimeUnit>) -> Result<(), String> {
        let descriptor = unit.descriptor();
        descriptor.validate()?;

        let mut guard =
            self.units.write().map_err(|_| "runtime unit registry lock poisoned".to_string())?;
        guard.insert(descriptor.unit_id, unit);
        Ok(())
    }

    pub fn get(&self, unit_id: &str) -> Result<Option<Arc<dyn RuntimeUnit>>, String> {
        let guard =
            self.units.read().map_err(|_| "runtime unit registry lock poisoned".to_string())?;
        Ok(guard.get(unit_id).cloned())
    }

    pub fn descriptors(&self) -> Result<Vec<RuntimeUnitDescriptor>, String> {
        let guard =
            self.units.read().map_err(|_| "runtime unit registry lock poisoned".to_string())?;
        Ok(guard.values().map(|unit| unit.descriptor()).collect())
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
                input_schema: "common/v1/envelope.capnp".to_string(),
                output_schema: "common/v1/envelope.capnp".to_string(),
                supports_wasm: true,
                supports_native: true,
                requires_shared_memory: false,
                supports_gpu: false,
                max_concurrency: 1,
            }
        }

        fn run(&self, input: &[u8]) -> Result<Vec<u8>, String> {
            Ok(input.to_vec())
        }
    }

    #[test]
    fn registers_and_reads_units() {
        let registry = UnitRegistry::default();
        registry.register(Arc::new(EchoUnit)).expect("register unit");

        let unit = registry.get("echo.compute").expect("registry access").expect("unit must exist");
        assert_eq!(unit.run(b"ping").expect("run unit"), b"ping");
    }
}
