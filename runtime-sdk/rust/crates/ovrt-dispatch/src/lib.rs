//! Lock-free compute placement over a fixed shared-memory lane table.
//!
//! One region serves every executor tier: a dual-buffered descriptor table
//! (membership, capabilities, locality Bloom sets), a global tick advanced by
//! `fetch_add`, and per-lane statistics owned exclusively by the executor
//! that reported them. Selection is a pure argmin over expected latency;
//! jurisdiction and trust pre-filters run before scoring and fail closed.
//!
//! The unsafe required to share the region lives entirely in
//! [`ovrt_core::mapping::SharedMapping`]. This crate composes that safe API,
//! so it forbids new unsafe outside the loom model cells.
//!
//! Layout constants come from `runtime_dispatch.capnp` through the generated
//! bindings in `ovrt-core`; Rust, Go, and TypeScript read identical bytes.

#![deny(unsafe_code)]

pub mod decide;

#[cfg(unix)]
pub mod block;
pub mod publisher;
#[cfg(unix)]
pub mod sample;

pub use decide::{blend_ewma, decide, DispatchRequest, LaneDescriptor, LaneStats, MAX_LANES};

#[cfg(unix)]
pub use block::DispatchBlock;
#[cfg(unix)]
pub use sample::{class_mask_for_role_index, SampledUnit};
