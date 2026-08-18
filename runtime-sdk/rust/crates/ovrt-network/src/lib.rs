//! `runtime-network`: the host-to-host rung of the runtime lane ladder.
//!
//! Every lane below this one moves bytes between two things that already trust
//! each other and already share an address space, a machine, or a pipe: `ffi`,
//! `shm`, `shm-epoch`, `stdio`. This crate is the first rung where the two ends
//! are separated by a network, and the first where the transport can be *wrong*
//! rather than merely slow — a path can drop a frame, stall, or vanish, and the
//! lane has to have an answer that is not "wait".
//!
//! Three answers live here, and they are deliberately the ones that need no new
//! dependency:
//!
//! - [`interface`] — which paths does this host actually have, and can a socket
//!   be pinned to one? Probed, never assumed.
//! - [`race`] — put a small frame on all of them and take the first delivery.
//!   Buys tail latency, costs duplicate bytes, and is therefore bounded to the
//!   4 KB control rung.
//! - [`dedup`] — drop the copies that racing creates, keyed on the idempotency
//!   key the envelope already carries.
//!
//! # What is deliberately not here
//!
//! **QUIC.** Racing and striping do not need it. It buys stream multiplexing,
//! 0-RTT resumption, and connection migration, none of which the rungs above
//! depend on — and it costs this workspace its single most unusual property,
//! which is that it has one non-optional third-party dependency. That trade may
//! be worth making; it is not worth making by accident, as a side effect of
//! wanting multipath.
//!
//! **Striping with erasure repair.** The rung above racing, for payloads where
//! sending twice is a bandwidth bill rather than a rounding error. [`race`]
//! refuses oversized frames instead of quietly doubling them, which is what
//! keeps this gap visible rather than silently mishandled.
//!
//! **Anything about satellites.** A host with two interfaces can race across
//! them. It cannot ask which physical medium carried a packet, and no socket API
//! exposes one.
//!
//! # Relationship to `kernellane`
//!
//! `server-kit/go/kernellane` already provides Multipath TCP, which is the
//! kernel doing a related job one layer down: a single connection striped across
//! interfaces by the OS. Where MPTCP is available it is the better tool for bulk
//! transfer and needs none of this. This crate covers what MPTCP does not — the
//! explicit, per-frame, application-visible race — and both feed the same lane
//! planner.

#![allow(unsafe_code)]
#![deny(unsafe_op_in_unsafe_fn)]

pub mod dedup;
pub mod interface;
pub mod race;

pub use dedup::{DedupRing, Observation};
pub use interface::{
    bind_socket_to_interface, enumerate, interface_binding_supported, NetworkInterface,
};
pub use race::{Attempt, Path, RaceOutcome, Racer, MAX_RACED_FRAME_BYTES};

/// A socket descriptor.
///
/// Aliased rather than using `RawFd` directly so the public API keeps the same
/// shape on platforms where `std::os::fd` does not exist. Callers on those
/// platforms get an honest [`NetworkError::Unsupported`] instead of a crate that
/// will not compile.
#[cfg(unix)]
pub type SocketFd = std::os::fd::RawFd;

/// A socket descriptor. See the unix definition for why this is an alias.
#[cfg(not(unix))]
pub type SocketFd = i32;

/// Why a network lane operation could not be completed.
///
/// Classified rather than stringly typed, because these separate cases a caller
/// must treat differently: `Unsupported` means stop asking, `Syscall` means
/// something about this host or moment, `FrameTooLarge` means use a different
/// rung, and `NoPaths` means a configuration mistake.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum NetworkError {
    /// The platform has no primitive for this. Permanent for this build; a
    /// caller should degrade to single-path rather than retry.
    Unsupported {
        /// What was attempted, phrased for a log line.
        what: &'static str,
    },
    /// A syscall failed. Carries the call name and `errno` so the cause survives
    /// into diagnostics instead of collapsing to "network error".
    Syscall {
        /// The syscall, including the option name where one applies.
        call: &'static str,
        /// The platform `errno` at the point of failure.
        errno: i32,
    },
    /// A frame exceeded the racing bound. Not a transport failure: the caller
    /// asked for the wrong rung of the payload ladder, and the fix is striping,
    /// not retrying.
    FrameTooLarge {
        /// The frame's size.
        bytes: usize,
        /// The bound it exceeded.
        limit: usize,
    },
    /// A racer was constructed with no paths.
    NoPaths,
    /// Every path is mid-send, so this frame could not be dispatched anywhere.
    ///
    /// Backpressure, not breakage: the paths are alive and will free up. A
    /// caller should retry or shed, not tear the racer down. Distinct from
    /// [`PathsUnavailable`](Self::PathsUnavailable) for exactly that reason.
    AllPathsBusy,
    /// Every worker in a racer has died, so no path could be dispatched to. The
    /// racer will not recover on its own.
    PathsUnavailable,
    /// A path's worker thread could not be started.
    WorkerSpawn {
        /// The path whose worker failed to start.
        label: String,
        /// The operating system's reason.
        reason: String,
    },
    /// A dedup ring was asked for a capacity it cannot honour.
    InvalidCapacity {
        /// The capacity requested.
        requested: usize,
    },
}

impl std::fmt::Display for NetworkError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Unsupported { what } => {
                write!(f, "{what} is not supported on this platform")
            }
            Self::Syscall { call, errno } => {
                write!(f, "{call} failed with errno {errno}")
            }
            Self::FrameTooLarge { bytes, limit } => write!(
                f,
                "frame of {bytes} bytes exceeds the {limit} byte racing bound; \
                 payloads above the control rung must be striped, not raced"
            ),
            Self::NoPaths => write!(f, "a racer needs at least one path"),
            Self::AllPathsBusy => {
                write!(f, "every path is mid-send; the frame was not dispatched")
            }
            Self::PathsUnavailable => write!(f, "every path worker has stopped"),
            Self::WorkerSpawn { label, reason } => {
                write!(f, "could not start the worker for path {label}: {reason}")
            }
            Self::InvalidCapacity { requested } => write!(
                f,
                "dedup capacity {requested} is invalid; it must be a power of two and at least 4"
            ),
        }
    }
}

impl std::error::Error for NetworkError {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn every_error_renders_its_cause() {
        // A transport error that reads as "network error" is a support ticket.
        // Each variant must name what happened and, where it exists, the number
        // that identifies it.
        let cases = [
            NetworkError::Unsupported { what: "binding" },
            NetworkError::Syscall { call: "setsockopt", errno: 1 },
            NetworkError::FrameTooLarge { bytes: 8192, limit: 4096 },
            NetworkError::NoPaths,
            NetworkError::AllPathsBusy,
            NetworkError::PathsUnavailable,
            NetworkError::WorkerSpawn {
                label: "eth0".to_string(),
                reason: "resource unavailable".to_string(),
            },
            NetworkError::InvalidCapacity { requested: 100 },
        ];

        for case in &cases {
            let rendered = case.to_string();
            assert!(!rendered.is_empty(), "{case:?} rendered empty");
            assert!(
                rendered.chars().any(char::is_alphabetic),
                "{case:?} rendered without a description"
            );
        }
    }

    #[test]
    fn the_oversized_frame_error_names_the_remedy() {
        // This error is the one a caller is most likely to hit, and the fix is
        // not obvious from the failure alone: the answer is a different rung of
        // the ladder, not a retry.
        let rendered = NetworkError::FrameTooLarge { bytes: 8192, limit: 4096 }.to_string();
        assert!(rendered.contains("8192"));
        assert!(rendered.contains("4096"));
        assert!(rendered.contains("striped"), "the error does not name the remedy");
    }
}
