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
