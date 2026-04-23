#![forbid(unsafe_code)]

use std::fmt::{Display, Formatter};
use std::str::FromStr;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum RuntimeRole {
    Pulse,
    Compute,
    Gpu,
    Io,
}

impl RuntimeRole {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Pulse => "pulse",
            Self::Compute => "compute",
            Self::Gpu => "gpu",
            Self::Io => "io",
        }
    }
}

impl Display for RuntimeRole {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

impl FromStr for RuntimeRole {
    type Err = String;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        match value {
            "pulse" => Ok(Self::Pulse),
            "compute" => Ok(Self::Compute),
            "gpu" => Ok(Self::Gpu),
            "io" => Ok(Self::Io),
            _ => Err(format!("unknown runtime role: {value}")),
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct RuntimeUnitDescriptor {
    pub unit_id: String,
    pub role: RuntimeRole,
    pub input_schema: String,
    pub output_schema: String,
    pub supports_wasm: bool,
    pub supports_native: bool,
    pub requires_shared_memory: bool,
    pub supports_gpu: bool,
    pub max_concurrency: u32,
}

impl RuntimeUnitDescriptor {
    pub fn validate(&self) -> Result<(), String> {
        if self.unit_id.trim().is_empty() {
            return Err("runtime unit id is required".to_string());
        }
        if self.input_schema.trim().is_empty() {
            return Err("runtime unit input schema is required".to_string());
        }
        if self.output_schema.trim().is_empty() {
            return Err("runtime unit output schema is required".to_string());
        }
        if self.max_concurrency == 0 {
            return Err("runtime unit max_concurrency must be positive".to_string());
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn validates_required_descriptor_fields() {
        let descriptor = RuntimeUnitDescriptor {
            unit_id: "preview.compute".to_string(),
            role: RuntimeRole::Compute,
            input_schema: "media/v1/asset.capnp".to_string(),
            output_schema: "preview/v1/layout.capnp".to_string(),
            supports_wasm: true,
            supports_native: true,
            requires_shared_memory: true,
            supports_gpu: false,
            max_concurrency: 2,
        };

        assert!(descriptor.validate().is_ok());
        assert_eq!(RuntimeRole::from_str("compute"), Ok(RuntimeRole::Compute));
    }
}
