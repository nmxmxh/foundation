#![allow(unsafe_code)]
#![deny(unsafe_op_in_unsafe_fn)]

pub mod binding_contract_gen;
pub mod diagnostics;
pub mod epoch;
pub mod generated;
pub mod layout;
pub mod log_ring;
pub mod mapping;
pub mod maplet;
pub mod native_gpu;
pub mod scratch;
pub mod soa_btree;
pub mod unit;

pub use diagnostics::*;
pub use epoch::{WaitError, WaitPolicy};
pub use generated::*;
pub use layout::*;
pub use maplet::*;
#[cfg(unix)]
pub use mapping::SharedMapping;
pub use native_gpu::*;
pub use scratch::*;
pub use soa_btree::*;
pub use unit::*;
