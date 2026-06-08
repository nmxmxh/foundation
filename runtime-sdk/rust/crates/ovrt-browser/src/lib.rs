#![deny(unsafe_op_in_unsafe_fn)]

pub mod buffer;
pub mod context;
pub mod js_interop;
pub mod logging;
pub mod ring_buffer;
pub mod signal;

pub use buffer::SafeBuffer;
pub use context::{init_context, is_context_valid};
pub use js_interop::{console_log, create_mock_buffer};
pub use logging::init_logging;
pub use ring_buffer::RuntimeRingBuffer;
pub use signal::Epoch;
