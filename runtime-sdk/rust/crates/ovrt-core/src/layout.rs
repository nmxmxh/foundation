use crate::generated::*;

pub const fn epoch_byte_offset(index: u32) -> u32 {
    OFFSET_EPOCHS + index.saturating_mul(EPOCH_SLOT_BYTES)
}

pub const fn header_byte_offset(index: u32) -> u32 {
    OFFSET_HEADER_INTS + index.saturating_mul(4)
}

pub fn validate_buffer_size(size: usize) -> Result<(), String> {
    if size < BUFFER_TOTAL_BYTES as usize {
        return Err(format!(
            "runtime buffer too small: {} < {}",
            size, BUFFER_TOTAL_BYTES
        ));
    }
    Ok(())
}

pub fn validate_region(offset: u32, length: u32, capacity: usize) -> Result<(), String> {
    let end = offset as usize + length as usize;
    if end > capacity {
        return Err(format!(
            "runtime region out of bounds: {} + {} > {}",
            offset, length, capacity
        ));
    }
    Ok(())
}

pub fn validate_input_length(length: u32) -> Result<(), String> {
    if length > INPUT_MAX_BYTES {
        return Err(format!(
            "input payload too large: {} > {}",
            length, INPUT_MAX_BYTES
        ));
    }
    Ok(())
}

pub fn validate_output_length(length: u32) -> Result<(), String> {
    if length > OUTPUT_MAX_BYTES {
        return Err(format!(
            "output payload too large: {} > {}",
            length, OUTPUT_MAX_BYTES
        ));
    }
    Ok(())
}

pub fn validate_diagnostic_length(length: u32) -> Result<(), String> {
    if length > DIAGNOSTIC_MAX_BYTES {
        return Err(format!(
            "diagnostic payload too large: {} > {}",
            length, DIAGNOSTIC_MAX_BYTES
        ));
    }
    Ok(())
}

pub const fn columnar_field_byte_offset(index: u32) -> u32 {
    COLUMNAR_BATCH_HEADER_BYTES + index.saturating_mul(COLUMNAR_FIELD_DESCRIPTOR_BYTES)
}

pub const fn columnar_descriptor_payload_bytes(column_count: u32) -> u32 {
    let raw =
        COLUMNAR_BATCH_HEADER_BYTES + column_count.saturating_mul(COLUMNAR_FIELD_DESCRIPTOR_BYTES);
    let align = COLUMNAR_BATCH_ALIGNMENT_BYTES;
    raw.div_ceil(align).saturating_mul(align)
}

pub fn validate_columnar_batch_shape(row_count: u32, column_count: u32) -> Result<(), String> {
    if column_count == 0 || column_count > COLUMNAR_BATCH_MAX_COLUMNS {
        return Err(format!(
            "invalid columnar batch column count: {}",
            column_count
        ));
    }
    if row_count == 0 {
        return Err("columnar batch row count is required".to_string());
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_offsets() {
        assert_eq!(epoch_byte_offset(0), 0);
        assert_eq!(epoch_byte_offset(1), 4);
        assert_eq!(header_byte_offset(0), 128);
        assert_eq!(header_byte_offset(1), 132);
    }

    #[test]
    fn test_validations() {
        assert!(validate_buffer_size(4096).is_ok());
        assert!(validate_buffer_size(4095).is_err());

        assert!(validate_region(256, 1024, 4096).is_ok());
        assert!(validate_region(4000, 100, 4096).is_err());

        assert!(validate_input_length(1024).is_ok());
        assert!(validate_input_length(1025).is_err());

        assert!(validate_output_length(2048).is_ok());
        assert!(validate_output_length(2049).is_err());

        assert!(validate_diagnostic_length(768).is_ok());
        assert!(validate_diagnostic_length(769).is_err());
    }

    #[test]
    fn test_columnar_descriptor_contract() {
        assert_eq!(ARENA_DESCRIPTOR_TYPE_COLUMNAR_BATCH, 5);
        assert_eq!(ARENA_DESCRIPTOR_TYPE_COLUMNAR_VALUES, 7);
        assert_eq!(COLUMNAR_BATCH_HEADER_BYTES, 32);
        assert_eq!(COLUMNAR_FIELD_DESCRIPTOR_BYTES, 64);
        assert_eq!(COLUMNAR_BATCH_ALIGNMENT_BYTES, 64);
        assert_eq!(columnar_field_byte_offset(0), 32);
        assert_eq!(columnar_field_byte_offset(1), 96);
        assert_eq!(columnar_descriptor_payload_bytes(1), 128);
        assert_eq!(columnar_descriptor_payload_bytes(2), 192);
        assert_eq!(COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID, 8);
        assert_eq!(COLUMNAR_DESCRIPTOR_ID_NONE, u32::MAX);
        assert!(validate_columnar_batch_shape(3, 2).is_ok());
        assert!(validate_columnar_batch_shape(0, 2).is_err());
        assert!(validate_columnar_batch_shape(3, 0).is_err());
        assert!(validate_columnar_batch_shape(3, COLUMNAR_BATCH_MAX_COLUMNS + 1).is_err());
    }
}
