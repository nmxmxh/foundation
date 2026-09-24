use ovrt_core::layout::{
    header_byte_offset, validate_buffer_size, validate_diagnostic_length, validate_input_length,
    validate_output_length, validate_region,
};
use ovrt_core::{
    BUFFER_SCHEMA_VERSION, EPOCH_SLOT_COUNT, INT_IDX_CONTEXT_HASH, INT_IDX_INPUT_LENGTH,
    INT_IDX_MODULE_VERSION, INT_IDX_OUTPUT_LENGTH, INT_IDX_RESERVED0, INT_IDX_RESERVED1,
    INT_IDX_SCHEMA_VERSION, INT_IDX_STATUS_CODE, OFFSET_DIAGNOSTIC_BYTES, OFFSET_INPUT_BYTES,
    OFFSET_OUTPUT_BYTES,
};

use crate::{js_interop, linear_buffer};

#[derive(Clone, Copy, Debug)]
pub struct SafeBuffer {
    handle: u32,
    capacity: usize,
}

impl SafeBuffer {
    pub fn new(handle: u32) -> Result<Self, String> {
        let capacity = if handle >= linear_buffer::LINEAR_HANDLE_START {
            linear_buffer::capacity(handle)?
        } else {
            js_interop::get_byte_length(handle) as usize
        };
        validate_buffer_size(capacity)?;
        Ok(Self { handle, capacity })
    }

    pub fn handle(&self) -> u32 {
        self.handle
    }

    pub fn capacity(&self) -> usize {
        self.capacity
    }

    pub fn initialize_control_plane(&self, module_version: i32) -> Result<(), String> {
        self.set_header_int(INT_IDX_SCHEMA_VERSION, BUFFER_SCHEMA_VERSION as i32)?;
        self.set_header_int(INT_IDX_INPUT_LENGTH, 0)?;
        self.set_header_int(INT_IDX_OUTPUT_LENGTH, 0)?;
        self.set_header_int(INT_IDX_STATUS_CODE, 0)?;
        self.set_header_int(INT_IDX_CONTEXT_HASH, 0)?;
        self.set_header_int(INT_IDX_MODULE_VERSION, module_version)?;
        self.set_header_int(INT_IDX_RESERVED0, 0)?;
        self.set_header_int(INT_IDX_RESERVED1, 0)?;
        Ok(())
    }

    pub fn read_at(&self, offset: u32, length: u32) -> Result<Vec<u8>, String> {
        validate_region(offset, length, self.capacity)?;
        if self.handle >= linear_buffer::LINEAR_HANDLE_START {
            return linear_buffer::with_bytes(self.handle, offset, length, |bytes| bytes.to_vec());
        }
        let mut bytes = vec![0; length as usize];
        js_interop::copy_from_buffer(self.handle, offset, &mut bytes);
        Ok(bytes)
    }

    /// Borrows published bytes until the callback returns. Nested buffer calls return a busy error.
    pub fn with_bytes<T>(
        &self,
        offset: u32,
        length: u32,
        read: impl FnOnce(&[u8]) -> T,
    ) -> Result<T, String> {
        validate_region(offset, length, self.capacity)?;
        if self.handle >= linear_buffer::LINEAR_HANDLE_START {
            return linear_buffer::with_bytes(self.handle, offset, length, read);
        }
        let bytes = self.read_at(offset, length)?;
        Ok(read(&bytes))
    }

    pub fn write_at(&self, offset: u32, bytes: &[u8]) -> Result<(), String> {
        let length = u32::try_from(bytes.len()).map_err(|_| "buffer write length exceeds u32")?;
        validate_region(offset, length, self.capacity)?;
        if self.handle >= linear_buffer::LINEAR_HANDLE_START {
            return linear_buffer::write(self.handle, offset, bytes);
        }
        js_interop::copy_to_buffer(self.handle, offset, bytes);
        Ok(())
    }

    pub fn header_int(&self, index: u32) -> Result<i32, String> {
        if index >= ovrt_core::HEADER_INT_COUNT {
            return Err(format!("invalid header index: {}", index));
        }
        self.with_bytes(header_byte_offset(index), 4, |bytes| {
            i32::from_le_bytes([bytes[0], bytes[1], bytes[2], bytes[3]])
        })
    }

    pub fn set_header_int(&self, index: u32, value: i32) -> Result<(), String> {
        if index >= ovrt_core::HEADER_INT_COUNT {
            return Err(format!("invalid header index: {}", index));
        }
        self.write_at(header_byte_offset(index), &value.to_le_bytes())
    }

    pub fn write_input_bytes(&self, bytes: &[u8]) -> Result<(), String> {
        validate_input_length(u32::try_from(bytes.len()).map_err(|_| "input length exceeds u32")?)?;
        self.write_at(OFFSET_INPUT_BYTES, bytes)?;
        self.set_header_int(INT_IDX_INPUT_LENGTH, bytes.len() as i32)?;
        Ok(())
    }

    pub fn read_input_bytes(&self) -> Result<Vec<u8>, String> {
        let length = u32::try_from(self.header_int(INT_IDX_INPUT_LENGTH)?)
            .map_err(|_| "negative input length")?;
        validate_input_length(length)?;
        self.read_at(OFFSET_INPUT_BYTES, length)
    }

    /// Borrows input without a payload copy on the linear-memory ABI.
    pub fn with_input_bytes<T>(&self, read: impl FnOnce(&[u8]) -> T) -> Result<T, String> {
        let length = u32::try_from(self.header_int(INT_IDX_INPUT_LENGTH)?)
            .map_err(|_| "negative input length")?;
        validate_input_length(length)?;
        self.with_bytes(OFFSET_INPUT_BYTES, length, read)
    }

    pub fn write_output_bytes(&self, bytes: &[u8]) -> Result<(), String> {
        validate_output_length(
            u32::try_from(bytes.len()).map_err(|_| "output length exceeds u32")?,
        )?;
        self.write_at(OFFSET_OUTPUT_BYTES, bytes)?;
        self.set_header_int(INT_IDX_OUTPUT_LENGTH, bytes.len() as i32)?;
        Ok(())
    }

    pub fn read_output_bytes(&self) -> Result<Vec<u8>, String> {
        let length = u32::try_from(self.header_int(INT_IDX_OUTPUT_LENGTH)?)
            .map_err(|_| "negative output length")?;
        validate_output_length(length)?;
        self.read_at(OFFSET_OUTPUT_BYTES, length)
    }

    pub fn write_diagnostic_bytes(&self, bytes: &[u8]) -> Result<(), String> {
        validate_diagnostic_length(
            u32::try_from(bytes.len()).map_err(|_| "diagnostic length exceeds u32")?,
        )?;
        self.write_at(OFFSET_DIAGNOSTIC_BYTES, bytes)
    }

    pub fn load_epoch(&self, index: u32) -> i32 {
        if index >= EPOCH_SLOT_COUNT {
            return 0;
        }
        if self.handle >= linear_buffer::LINEAR_HANDLE_START {
            return linear_buffer::load(self.handle, index);
        }
        js_interop::atomic_load(self.handle, index)
    }

    pub fn store_epoch(&self, index: u32, value: i32) -> i32 {
        if index >= EPOCH_SLOT_COUNT {
            return 0;
        }
        if self.handle >= linear_buffer::LINEAR_HANDLE_START {
            return linear_buffer::store(self.handle, index, value);
        }
        js_interop::atomic_store(self.handle, index, value)
    }

    pub fn add_epoch(&self, index: u32, delta: i32) -> i32 {
        if index >= EPOCH_SLOT_COUNT {
            return 0;
        }
        if self.handle >= linear_buffer::LINEAR_HANDLE_START {
            return linear_buffer::add(self.handle, index, delta);
        }
        js_interop::atomic_add(self.handle, index, delta)
    }

    pub fn compare_exchange_epoch(&self, index: u32, expected: i32, replacement: i32) -> i32 {
        if index >= EPOCH_SLOT_COUNT {
            return 0;
        }
        if self.handle >= linear_buffer::LINEAR_HANDLE_START {
            return linear_buffer::compare_exchange(self.handle, index, expected, replacement);
        }
        js_interop::atomic_compare_exchange(self.handle, index, expected, replacement)
    }

    pub fn notify_epoch(&self, index: u32, count: i32) -> i32 {
        if index >= EPOCH_SLOT_COUNT {
            return 0;
        }
        js_interop::atomic_notify(self.handle, index, count)
    }
}

#[cfg(test)]
mod tests {
    use ovrt_core::{
        BUFFER_TOTAL_BYTES, DIAGNOSTIC_MAX_BYTES, IDX_INPUT_WRITTEN, IDX_OUTPUT_CONSUMED,
        IDX_OUTPUT_WRITTEN, INPUT_MAX_BYTES, OUTPUT_MAX_BYTES,
    };

    use super::*;

    #[test]
    fn round_trips_control_regions() {
        let handle = crate::js_interop::create_mock_buffer(BUFFER_TOTAL_BYTES as usize);
        let buffer = SafeBuffer::new(handle).expect("create buffer");
        buffer.initialize_control_plane(7).expect("init");
        buffer.write_input_bytes(b"asset").expect("write input");
        buffer.write_output_bytes(b"layout").expect("write output");
        buffer.write_diagnostic_bytes(b"diagnostic").expect("write diagnostics");

        assert_eq!(buffer.read_input_bytes().expect("read input"), b"asset");
        assert_eq!(buffer.read_output_bytes().expect("read output"), b"layout");
        assert_eq!(buffer.header_int(INT_IDX_MODULE_VERSION).expect("module version"), 7);

        assert_eq!(buffer.add_epoch(IDX_INPUT_WRITTEN, 1), 0);
        assert_eq!(buffer.add_epoch(IDX_OUTPUT_WRITTEN, 1), 0);
        assert_eq!(buffer.add_epoch(IDX_OUTPUT_CONSUMED, 1), 0);
    }

    #[test]
    fn rejects_payloads_that_exceed_region_capacity() {
        let handle = crate::js_interop::create_mock_buffer(BUFFER_TOTAL_BYTES as usize);
        let buffer = SafeBuffer::new(handle).expect("create buffer");
        assert!(buffer.write_input_bytes(&vec![0; INPUT_MAX_BYTES as usize + 1]).is_err());
        assert!(buffer.write_output_bytes(&vec![0; OUTPUT_MAX_BYTES as usize + 1]).is_err());
        assert!(buffer
            .write_diagnostic_bytes(&vec![0; DIAGNOSTIC_MAX_BYTES as usize + 1])
            .is_err());
    }
}
