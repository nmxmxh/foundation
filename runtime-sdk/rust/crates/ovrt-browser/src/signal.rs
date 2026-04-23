use ovrt_core::{
    IDX_DIAGNOSTICS_WRITTEN, IDX_INPUT_WRITTEN, IDX_KERNEL_READY, IDX_OUTPUT_CONSUMED,
    IDX_OUTPUT_WRITTEN, IDX_PANIC_STATE,
};

use crate::buffer::SafeBuffer;

#[derive(Clone, Copy, Debug)]
pub struct Epoch {
    buffer: SafeBuffer,
    index: u32,
    last_seen: i32,
}

impl Epoch {
    pub fn new(buffer: SafeBuffer, index: u32) -> Self {
        let last_seen = buffer.load_epoch(index);
        Self { buffer, index, last_seen }
    }

    pub fn current(&self) -> i32 {
        self.buffer.load_epoch(self.index)
    }

    pub fn increment(&mut self) -> i32 {
        self.buffer.add_epoch(self.index, 1) + 1
    }

    pub fn has_changed(&mut self) -> bool {
        let current = self.current();
        if current > self.last_seen {
            self.last_seen = current;
            return true;
        }
        false
    }
}

pub fn mark_kernel_ready(buffer: SafeBuffer) -> i32 {
    buffer.store_epoch(IDX_KERNEL_READY, 1)
}

pub fn mark_input_written(buffer: SafeBuffer) -> i32 {
    buffer.add_epoch(IDX_INPUT_WRITTEN, 1) + 1
}

pub fn mark_output_written(buffer: SafeBuffer) -> i32 {
    buffer.add_epoch(IDX_OUTPUT_WRITTEN, 1) + 1
}

pub fn mark_output_consumed(buffer: SafeBuffer) -> i32 {
    buffer.add_epoch(IDX_OUTPUT_CONSUMED, 1) + 1
}

pub fn mark_panic_state(buffer: SafeBuffer) -> i32 {
    buffer.store_epoch(IDX_PANIC_STATE, 1)
}

pub fn mark_diagnostics_written(buffer: SafeBuffer) -> i32 {
    buffer.add_epoch(IDX_DIAGNOSTICS_WRITTEN, 1) + 1
}

#[cfg(test)]
mod tests {
    use ovrt_core::BUFFER_TOTAL_BYTES;

    use super::*;

    #[test]
    fn detects_epoch_changes() {
        let handle = crate::js_interop::create_mock_buffer(BUFFER_TOTAL_BYTES as usize);
        let buffer = SafeBuffer::new(handle).expect("create buffer");
        let mut epoch = Epoch::new(buffer, IDX_OUTPUT_WRITTEN);
        assert!(!epoch.has_changed());
        assert_eq!(epoch.increment(), 1);
        assert!(epoch.has_changed());
        assert!(!epoch.has_changed());
    }
}
