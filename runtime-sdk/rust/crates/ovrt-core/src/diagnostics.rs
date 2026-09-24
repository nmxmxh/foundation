#![forbid(unsafe_code)]

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum RuntimeMode {
    Stopped,
    Worker,
    MainThread,
    Native,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct RuntimeDiagnostics {
    pub mode: RuntimeMode,
    pub degraded: bool,
    pub active_units: u32,
    pub in_flight: u32,
    pub last_runtime_source: String,
    pub last_error: Option<String>,
    pub last_epoch: u32,
}

impl Default for RuntimeDiagnostics {
    fn default() -> Self {
        Self {
            mode: RuntimeMode::Stopped,
            degraded: false,
            active_units: 0,
            in_flight: 0,
            last_runtime_source: "idle".to_string(),
            last_error: None,
            last_epoch: 0,
        }
    }
}

impl RuntimeDiagnostics {
    /// Reuses source storage across completed requests.
    pub fn set_runtime_source(&mut self, source: &str) {
        self.last_runtime_source.clear();
        self.last_runtime_source.push_str(source);
    }
}

#[cfg(test)]
mod tests {
    use super::RuntimeDiagnostics;

    #[test]
    fn source_updates_reuse_capacity_and_replace_visible_text() {
        let mut diagnostics = RuntimeDiagnostics::default();
        diagnostics.set_runtime_source("native-ffi-error");
        let pointer = diagnostics.last_runtime_source.as_ptr();
        diagnostics.set_runtime_source("native-ffi");
        assert_eq!(diagnostics.last_runtime_source, "native-ffi");
        assert_eq!(diagnostics.last_runtime_source.as_ptr(), pointer);
        diagnostics.set_runtime_source("");
        assert!(diagnostics.last_runtime_source.is_empty());
    }
}
