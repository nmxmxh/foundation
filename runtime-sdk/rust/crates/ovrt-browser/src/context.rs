use std::sync::atomic::{AtomicI32, Ordering};

use ovrt_core::INT_IDX_CONTEXT_HASH;

use crate::buffer::SafeBuffer;
use crate::js_interop;

static INITIAL_CONTEXT_HASH: AtomicI32 = AtomicI32::new(0);

pub fn init_context(buffer: SafeBuffer) {
    let current = buffer.header_int(INT_IDX_CONTEXT_HASH).unwrap_or(0);
    if current == 0 {
        return;
    }
    if INITIAL_CONTEXT_HASH
        .compare_exchange(0, current, Ordering::SeqCst, Ordering::SeqCst)
        .is_ok()
    {
        js_interop::console_log(
            &format!("[ovrt-browser] initialized context hash {}", current),
            2,
        );
    }
}

pub fn is_context_valid(buffer: SafeBuffer) -> bool {
    let initial = INITIAL_CONTEXT_HASH.load(Ordering::Relaxed);
    if initial == 0 {
        return true;
    }
    let current = buffer.header_int(INT_IDX_CONTEXT_HASH).unwrap_or(0);
    if current == 0 {
        return true;
    }
    current == initial
}
