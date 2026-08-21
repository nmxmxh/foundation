//! Bounded execution budgets for both parity lanes.
//!
//! Every field maps to a hard stop, not an advisory. The WASM lane enforces
//! fuel and memory pages inside the engine and the deadline through epoch
//! interruption. The native lane enforces payload ceilings before the unit
//! runs. A guest that exceeds any bound fails its exchange; it never degrades
//! the host.

#![forbid(unsafe_code)]

use ovrt_core::{INPUT_MAX_BYTES, OUTPUT_MAX_BYTES};

/// One WASM linear-memory page, 64 KiB.
pub const WASM_PAGE_BYTES: u64 = 65_536;

/// Execution budgets applied identically to each lane.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ResourceLimits {
    /// Largest accepted input payload in bytes.
    pub max_input_bytes: u32,
    /// Largest accepted output payload in bytes.
    pub max_output_bytes: u32,
    /// Largest guest linear memory in WASM pages.
    pub max_memory_pages: u32,
    /// Wall-clock budget for one guest exchange in milliseconds.
    pub timeout_ms: u64,
    /// Engine instruction budget for one guest exchange.
    pub max_fuel: u64,
}

impl Default for ResourceLimits {
    fn default() -> Self {
        Self::for_compute()
    }
}

impl ResourceLimits {
    /// Budget for short deterministic compute units.
    pub fn for_compute() -> Self {
        Self {
            max_input_bytes: INPUT_MAX_BYTES,
            max_output_bytes: OUTPUT_MAX_BYTES,
            max_memory_pages: 256,
            timeout_ms: 5_000,
            max_fuel: 100_000_000,
        }
    }

    /// Budget for heavier units such as bulk crypto transforms.
    pub fn for_crypto() -> Self {
        Self { timeout_ms: 30_000, max_fuel: 10_000_000_000, ..Self::for_compute() }
    }

    /// Rejects zero bounds and payloads beyond the control-buffer contract.
    pub fn validate(&self) -> Result<(), String> {
        if self.max_input_bytes == 0 || self.max_input_bytes > INPUT_MAX_BYTES {
            return Err(format!(
                "max_input_bytes must be within 1..={INPUT_MAX_BYTES}: {}",
                self.max_input_bytes
            ));
        }
        if self.max_output_bytes == 0 || self.max_output_bytes > OUTPUT_MAX_BYTES {
            return Err(format!(
                "max_output_bytes must be within 1..={OUTPUT_MAX_BYTES}: {}",
                self.max_output_bytes
            ));
        }
        if self.max_memory_pages == 0 {
            return Err("max_memory_pages must be positive".to_string());
        }
        if self.timeout_ms == 0 {
            return Err("timeout_ms must be positive".to_string());
        }
        if self.max_fuel == 0 {
            return Err("max_fuel must be positive".to_string());
        }
        Ok(())
    }

    /// Reports whether an input payload fits this budget.
    pub fn admits_input(&self, len: usize) -> bool {
        len as u32 <= self.max_input_bytes
    }

    /// Reports whether an output payload fits this budget.
    pub fn admits_output(&self, len: usize) -> bool {
        len as u32 <= self.max_output_bytes
    }

    /// Guest memory ceiling in bytes, for limiter wiring.
    pub fn memory_ceiling_bytes(&self) -> u64 {
        u64::from(self.max_memory_pages) * WASM_PAGE_BYTES
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn presets_satisfy_validation() {
        assert!(ResourceLimits::for_compute().validate().is_ok());
        assert!(ResourceLimits::for_crypto().validate().is_ok());
    }

    #[test]
    fn validation_rejects_zero_and_oversized_bounds() {
        let mut limits = ResourceLimits::for_compute();
        limits.timeout_ms = 0;
        assert!(limits.validate().is_err());

        let mut limits = ResourceLimits::for_compute();
        limits.max_input_bytes = INPUT_MAX_BYTES + 1;
        assert!(limits.validate().is_err());

        let mut limits = ResourceLimits::for_compute();
        limits.max_fuel = 0;
        assert!(limits.validate().is_err());
    }

    #[test]
    fn admission_follows_declared_budgets() {
        let limits = ResourceLimits { max_input_bytes: 8, ..ResourceLimits::for_compute() };
        assert!(limits.admits_input(8));
        assert!(!limits.admits_input(9));
    }
}
