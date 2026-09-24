//! Successful calls update a bounded shard instead of one shared diagnostic lock.

use std::sync::atomic::{AtomicBool, AtomicU32, AtomicU8, AtomicUsize, Ordering};
use std::sync::Mutex;

use ovrt_core::{RuntimeDiagnostics, RuntimeMode};

const SHARDS: usize = 64;
static NEXT_SLOT: AtomicUsize = AtomicUsize::new(0);
thread_local! {
    static SLOT: usize = NEXT_SLOT.fetch_add(1, Ordering::Relaxed) % SHARDS;
}

#[derive(Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub(crate) enum Source {
    Idle,
    Native,
    Ffi,
    StartupError,
    NativeError,
    FfiError,
    Timeout,
    Disconnected,
}

impl Source {
    fn label(value: u8) -> &'static str {
        match value {
            1 => "native",
            2 => "native-ffi",
            3 => "native-startup-error",
            4 => "native-error",
            5 => "native-ffi-error",
            6 => "native-timeout",
            7 => "native-disconnected",
            _ => "idle",
        }
    }
}

#[derive(Default)]
#[repr(align(128))]
struct Shard {
    completed: AtomicU32,
    active: AtomicU32,
}

pub(crate) struct NativeDiagnostics {
    shards: [Shard; SHARDS],
    source: AtomicU8,
    error: Mutex<Option<String>>,
    degraded: AtomicBool,
    pub active_units: AtomicU32,
}

impl Default for NativeDiagnostics {
    fn default() -> Self {
        Self {
            shards: std::array::from_fn(|_| Shard::default()),
            source: AtomicU8::new(Source::Idle as u8),
            error: Mutex::new(None),
            degraded: AtomicBool::new(false),
            active_units: AtomicU32::new(0),
        }
    }
}

impl NativeDiagnostics {
    pub fn begin(&self) -> ActiveCall<'_> {
        let shard = &self.shards[SLOT.with(|slot| *slot)];
        shard.active.fetch_add(1, Ordering::Relaxed);
        ActiveCall(shard)
    }

    pub fn success(&self, source: Source) -> Result<(), String> {
        let shard = &self.shards[SLOT.with(|slot| *slot)];
        let _ = shard.completed.fetch_update(Ordering::Relaxed, Ordering::Relaxed, |count| {
            Some(count.saturating_add(1))
        });
        if self.source.load(Ordering::Acquire) != source as u8 {
            let mut error =
                self.error.lock().map_err(|_| "runtime diagnostics lock poisoned".to_string())?;
            *error = None;
            self.source.store(source as u8, Ordering::Release);
        }
        Ok(())
    }

    pub fn failure(&self, source: Source, message: &str) {
        if let Ok(mut error) = self.error.lock() {
            *error = Some(message.to_owned());
            self.degraded.store(true, Ordering::Relaxed);
            self.source.store(source as u8, Ordering::Release);
        }
    }

    pub fn snapshot(&self, queued: u32) -> Result<RuntimeDiagnostics, String> {
        let error =
            self.error.lock().map_err(|_| "runtime diagnostics lock poisoned".to_string())?;
        let (mut completed, mut active) = (0_u32, queued);
        for shard in &self.shards {
            completed = completed.saturating_add(shard.completed.load(Ordering::Relaxed));
            active = active.saturating_add(shard.active.load(Ordering::Relaxed));
        }
        Ok(RuntimeDiagnostics {
            mode: RuntimeMode::Native,
            active_units: self.active_units.load(Ordering::Relaxed),
            in_flight: active,
            last_epoch: completed,
            last_runtime_source: Source::label(self.source.load(Ordering::Acquire)).to_owned(),
            last_error: error.clone(),
            degraded: self.degraded.load(Ordering::Relaxed),
        })
    }
}

pub(crate) struct ActiveCall<'a>(&'a Shard);

impl Drop for ActiveCall<'_> {
    fn drop(&mut self) {
        self.0.active.fetch_sub(1, Ordering::Relaxed);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn concurrent_completions_count_exactly_and_errors_clear_without_losing_degradation() {
        let diagnostics = NativeDiagnostics::default();
        std::thread::scope(|scope| {
            for _ in 0..8 {
                let diagnostics = &diagnostics;
                scope.spawn(move || {
                    for _ in 0..1_000 {
                        let _call = diagnostics.begin();
                        diagnostics.success(Source::Ffi).expect("success");
                    }
                });
            }
        });
        let state = diagnostics.snapshot(0).expect("snapshot");
        assert_eq!((state.last_epoch, state.in_flight), (8_000, 0));
        diagnostics.failure(Source::FfiError, "failed");
        assert_eq!(diagnostics.snapshot(0).expect("failure").last_error.as_deref(), Some("failed"));
        diagnostics.success(Source::Ffi).expect("recover");
        let state = diagnostics.snapshot(0).expect("recovered");
        assert!(state.degraded);
        assert!(state.last_error.is_none());
        assert_eq!(state.last_epoch, 8_001);
    }

    #[test]
    fn epochs_saturate_and_active_calls_restore_during_unwind() {
        let diagnostics = NativeDiagnostics::default();
        diagnostics.shards[SLOT.with(|slot| *slot)].completed.store(u32::MAX, Ordering::Relaxed);
        diagnostics.success(Source::Native).expect("saturate");
        let _ = std::panic::catch_unwind(|| {
            let _call = diagnostics.begin();
            assert_eq!(diagnostics.snapshot(3).expect("active").in_flight, 4);
            panic!("test unwind");
        });
        let state = diagnostics.snapshot(0).expect("after unwind");
        assert_eq!((state.last_epoch, state.in_flight), (u32::MAX, 0));
    }
}

#[cfg(test)]
#[cfg(feature = "loom")]
mod loom_verification {
    use loom::sync::atomic::{AtomicU8, Ordering};
    use loom::sync::{Arc, Mutex};

    #[test]
    fn source_and_error_remain_coherent_during_recovery() {
        loom::model(|| {
            let state = Arc::new((AtomicU8::new(1), Mutex::new(false)));
            let failure = Arc::clone(&state);
            let fail = loom::thread::spawn(move || {
                let mut error = failure.1.lock().expect("failure");
                *error = true;
                failure.0.store(2, Ordering::Release);
            });
            let success = Arc::clone(&state);
            let recover = loom::thread::spawn(move || {
                if success.0.load(Ordering::Acquire) != 1 {
                    let mut error = success.1.lock().expect("recovery");
                    *error = false;
                    success.0.store(1, Ordering::Release);
                }
            });
            {
                let error = state.1.lock().expect("snapshot");
                assert_eq!(*error, state.0.load(Ordering::Acquire) == 2);
            }
            fail.join().expect("failure completion");
            recover.join().expect("recovery completion");
            let error = state.1.lock().expect("final snapshot");
            assert_eq!(*error, state.0.load(Ordering::Acquire) == 2);
        });
    }
}
