use ovrt_core::layout::{
    header_byte_offset, validate_buffer_size, validate_diagnostic_length, validate_input_length,
    validate_output_length, validate_region,
};
use ovrt_core::{
    BUFFER_SCHEMA_VERSION, DIAGNOSTIC_MAX_BYTES, EPOCH_SLOT_BYTES, EPOCH_SLOT_COUNT,
    INT_IDX_CONTEXT_HASH, INT_IDX_INPUT_LENGTH, INT_IDX_MODULE_VERSION, INT_IDX_OUTPUT_LENGTH,
    INT_IDX_RESERVED0, INT_IDX_RESERVED1, INT_IDX_SCHEMA_VERSION, INT_IDX_STATUS_CODE,
    OFFSET_DIAGNOSTIC_BYTES, OFFSET_EPOCHS, OFFSET_INPUT_BYTES, OFFSET_OUTPUT_BYTES,
};

#[derive(Debug, Clone)]
pub struct NativeBuffer {
    raw: Vec<u8>,
}

impl NativeBuffer {
    pub fn new(raw: Vec<u8>) -> Result<Self, String> {
        validate_buffer_size(raw.len())?;
        Ok(Self { raw })
    }

    pub fn with_capacity() -> Self {
        Self { raw: vec![0; ovrt_core::BUFFER_TOTAL_BYTES as usize] }
    }

    pub fn into_inner(self) -> Vec<u8> {
        self.raw
    }

    pub fn raw_bytes(&self) -> &[u8] {
        self.raw.as_slice()
    }

    pub fn reset(&mut self) {
        self.raw.fill(0);
    }

    pub fn initialize_control_plane(&mut self, module_version: i32) -> Result<(), String> {
        self.set_header_int(INT_IDX_SCHEMA_VERSION, BUFFER_SCHEMA_VERSION as i32)?;
        self.set_header_int(INT_IDX_INPUT_LENGTH, 0)?;
        self.set_header_int(INT_IDX_OUTPUT_LENGTH, 0)?;
        self.set_header_int(INT_IDX_STATUS_CODE, 0)?;
        self.set_header_int(INT_IDX_CONTEXT_HASH, 0)?;
        self.set_header_int(INT_IDX_MODULE_VERSION, module_version)?;
        self.set_header_int(INT_IDX_RESERVED0, 0)?;
        self.set_header_int(INT_IDX_RESERVED1, 0)?;
        self.store_epoch(ovrt_core::IDX_KERNEL_READY, 1)?;
        Ok(())
    }

    pub fn header_int(&self, index: u32) -> Result<i32, String> {
        if index >= ovrt_core::HEADER_INT_COUNT {
            return Err(format!("invalid header index: {index}"));
        }
        let offset = header_byte_offset(index) as usize;
        let bytes: [u8; 4] = self.raw[offset..offset + 4]
            .try_into()
            .map_err(|_| "header integer region must be exactly four bytes".to_string())?;
        Ok(i32::from_le_bytes(bytes))
    }

    pub fn set_header_int(&mut self, index: u32, value: i32) -> Result<(), String> {
        if index >= ovrt_core::HEADER_INT_COUNT {
            return Err(format!("invalid header index: {index}"));
        }
        let offset = header_byte_offset(index) as usize;
        self.raw[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
        Ok(())
    }

    pub fn read_input_bytes(&self) -> Result<Vec<u8>, String> {
        let length = self.header_int(INT_IDX_INPUT_LENGTH)?.max(0) as u32;
        validate_input_length(length)?;
        self.read_region(OFFSET_INPUT_BYTES, length)
    }

    pub fn write_input_bytes(&mut self, bytes: &[u8]) -> Result<(), String> {
        validate_input_length(bytes.len() as u32)?;
        self.zero_region(OFFSET_INPUT_BYTES, ovrt_core::INPUT_MAX_BYTES)?;
        self.write_region(OFFSET_INPUT_BYTES, bytes)?;
        self.set_header_int(INT_IDX_INPUT_LENGTH, bytes.len() as i32)
    }

    pub fn read_output_bytes(&self) -> Result<Vec<u8>, String> {
        let length = self.header_int(INT_IDX_OUTPUT_LENGTH)?.max(0) as u32;
        validate_output_length(length)?;
        self.read_region(OFFSET_OUTPUT_BYTES, length)
    }

    pub fn write_output_bytes(&mut self, bytes: &[u8]) -> Result<(), String> {
        validate_output_length(bytes.len() as u32)?;
        self.zero_region(OFFSET_OUTPUT_BYTES, ovrt_core::OUTPUT_MAX_BYTES)?;
        self.write_region(OFFSET_OUTPUT_BYTES, bytes)?;
        self.set_header_int(INT_IDX_OUTPUT_LENGTH, bytes.len() as i32)
    }

    pub fn clear_output(&mut self) -> Result<(), String> {
        self.zero_region(OFFSET_OUTPUT_BYTES, ovrt_core::OUTPUT_MAX_BYTES)?;
        self.set_header_int(INT_IDX_OUTPUT_LENGTH, 0)
    }

    pub fn diagnostics_text(&self) -> String {
        let start = OFFSET_DIAGNOSTIC_BYTES as usize;
        let end = start + DIAGNOSTIC_MAX_BYTES as usize;
        String::from_utf8_lossy(&self.raw[start..end]).trim_end_matches('\0').to_string()
    }

    pub fn set_diagnostics_text(&mut self, message: &str) -> Result<(), String> {
        let bytes = message.as_bytes();
        validate_diagnostic_length(bytes.len() as u32)?;
        self.zero_region(OFFSET_DIAGNOSTIC_BYTES, DIAGNOSTIC_MAX_BYTES)?;
        self.write_region(OFFSET_DIAGNOSTIC_BYTES, bytes)?;
        let _ = self.add_epoch(ovrt_core::IDX_DIAGNOSTICS_WRITTEN, 1)?;
        Ok(())
    }

    pub fn load_epoch(&self, index: u32) -> i32 {
        if index >= EPOCH_SLOT_COUNT {
            return 0;
        }
        let offset = (OFFSET_EPOCHS + index * EPOCH_SLOT_BYTES) as usize;
        let bytes: [u8; 4] = self.raw[offset..offset + 4].try_into().unwrap_or_default();
        i32::from_le_bytes(bytes)
    }

    pub fn store_epoch(&mut self, index: u32, value: i32) -> Result<(), String> {
        if index >= EPOCH_SLOT_COUNT {
            return Err(format!("invalid epoch index: {index}"));
        }
        let offset = (OFFSET_EPOCHS + index * EPOCH_SLOT_BYTES) as usize;
        self.raw[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
        Ok(())
    }

    pub fn add_epoch(&mut self, index: u32, delta: i32) -> Result<i32, String> {
        let current = self.load_epoch(index);
        self.store_epoch(index, current.saturating_add(delta))?;
        Ok(current)
    }

    fn read_region(&self, offset: u32, length: u32) -> Result<Vec<u8>, String> {
        validate_region(offset, length, self.raw.len())?;
        let start = offset as usize;
        let end = start + length as usize;
        Ok(self.raw[start..end].to_vec())
    }

    fn write_region(&mut self, offset: u32, bytes: &[u8]) -> Result<(), String> {
        validate_region(offset, bytes.len() as u32, self.raw.len())?;
        let start = offset as usize;
        let end = start + bytes.len();
        self.raw[start..end].copy_from_slice(bytes);
        Ok(())
    }

    fn zero_region(&mut self, offset: u32, length: u32) -> Result<(), String> {
        validate_region(offset, length, self.raw.len())?;
        let start = offset as usize;
        let end = start + length as usize;
        self.raw[start..end].fill(0);
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use ovrt_core::{BUFFER_TOTAL_BYTES, IDX_INPUT_WRITTEN, IDX_OUTPUT_WRITTEN};

    use super::*;

    #[test]
    fn round_trips_buffer_regions_and_epochs() {
        let mut buffer = NativeBuffer::new(vec![0; BUFFER_TOTAL_BYTES as usize]).expect("buffer");
        buffer.initialize_control_plane(9).expect("init");
        buffer.write_input_bytes(b"asset").expect("write input");
        buffer.write_output_bytes(b"layout").expect("write output");
        buffer.set_diagnostics_text("degraded").expect("write diagnostics");
        let _ = buffer.add_epoch(IDX_INPUT_WRITTEN, 1).expect("input epoch");
        let _ = buffer.add_epoch(IDX_OUTPUT_WRITTEN, 1).expect("output epoch");

        assert_eq!(buffer.read_input_bytes().expect("read input"), b"asset");
        assert_eq!(buffer.read_output_bytes().expect("read output"), b"layout");
        assert_eq!(buffer.diagnostics_text(), "degraded");
        assert_eq!(buffer.load_epoch(IDX_INPUT_WRITTEN), 1);
        assert_eq!(buffer.load_epoch(IDX_OUTPUT_WRITTEN), 1);
    }
}
