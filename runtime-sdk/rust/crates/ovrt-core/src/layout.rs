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
}
