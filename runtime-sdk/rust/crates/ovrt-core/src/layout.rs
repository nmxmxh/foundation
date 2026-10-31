use crate::generated::*;
use crate::scratch::ScratchArena;

pub const fn epoch_byte_offset(index: u32) -> u32 {
    OFFSET_EPOCHS + index.saturating_mul(EPOCH_SLOT_BYTES)
}

pub const fn header_byte_offset(index: u32) -> u32 {
    OFFSET_HEADER_INTS + index.saturating_mul(4)
}

pub fn validate_buffer_size(size: usize) -> Result<(), String> {
    if size < BUFFER_TOTAL_BYTES as usize {
        return Err(format!("runtime buffer too small: {} < {}", size, BUFFER_TOTAL_BYTES));
    }
    Ok(())
}

pub fn validate_region(offset: u32, length: u32, capacity: usize) -> Result<(), String> {
    let start = offset as usize;
    let end = start.checked_add(length as usize).ok_or_else(|| {
        format!("runtime region offset overflow: {} + {} exceeds addressable space", offset, length)
    })?;
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
        return Err(format!("input payload too large: {} > {}", length, INPUT_MAX_BYTES));
    }
    Ok(())
}

pub fn validate_output_length(length: u32) -> Result<(), String> {
    if length > OUTPUT_MAX_BYTES {
        return Err(format!("output payload too large: {} > {}", length, OUTPUT_MAX_BYTES));
    }
    Ok(())
}

pub fn validate_diagnostic_length(length: u32) -> Result<(), String> {
    if length > DIAGNOSTIC_MAX_BYTES {
        return Err(format!("diagnostic payload too large: {} > {}", length, DIAGNOSTIC_MAX_BYTES));
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
        return Err(format!("invalid columnar batch column count: {}", column_count));
    }
    if row_count == 0 {
        return Err("columnar batch row count is required".to_string());
    }
    Ok(())
}

/// ColumnarFieldSpec defines column metadata for batch assembly.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ColumnarFieldSpec {
    pub field_id: u32,
    pub logical_type: u32,
    pub physical_type: u32,
    pub flags: u32,
    pub length: u32,
    pub byte_width: u32,
    pub values_descriptor_id: u32,
}

impl ColumnarFieldSpec {
    /// Creates a fixed-width columnar field specification.
    pub fn new_fixed(
        field_id: u32,
        logical_type: u32,
        length: u32,
        byte_width: u32,
        values_descriptor_id: u32,
    ) -> Self {
        Self {
            field_id,
            logical_type,
            physical_type: COLUMNAR_PHYSICAL_TYPE_FIXED_WIDTH,
            flags: 0,
            length,
            byte_width,
            values_descriptor_id,
        }
    }
}

/// Assembles a columnar batch header and field descriptors into a destination slice.
pub fn assemble_columnar_batch_into(
    row_count: u32,
    fields: &[ColumnarFieldSpec],
    dest: &mut [u8],
) -> Result<usize, String> {
    validate_columnar_batch_shape(row_count, fields.len() as u32)?;
    let total_bytes = columnar_descriptor_payload_bytes(fields.len() as u32) as usize;
    if dest.len() < total_bytes {
        return Err(format!(
            "destination buffer too small for columnar batch: {} < {}",
            dest.len(),
            total_bytes
        ));
    }

    dest[..total_bytes].fill(0);

    let put_u32 = |slice: &mut [u8], offset: usize, val: u32| {
        slice[offset..offset + 4].copy_from_slice(&val.to_le_bytes());
    };

    put_u32(dest, (COLUMNAR_BATCH_HEADER_IDX_MAGIC * 4) as usize, COLUMNAR_BATCH_MAGIC);
    put_u32(
        dest,
        (COLUMNAR_BATCH_HEADER_IDX_SCHEMA_VERSION * 4) as usize,
        COLUMNAR_BATCH_SCHEMA_VERSION,
    );
    put_u32(dest, (COLUMNAR_BATCH_HEADER_IDX_ROW_COUNT * 4) as usize, row_count);
    put_u32(dest, (COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT * 4) as usize, fields.len() as u32);
    put_u32(dest, (COLUMNAR_BATCH_HEADER_IDX_FLAGS * 4) as usize, 0);
    put_u32(
        dest,
        (COLUMNAR_BATCH_HEADER_IDX_METADATA_DESCRIPTOR_ID * 4) as usize,
        COLUMNAR_DESCRIPTOR_ID_NONE,
    );
    put_u32(
        dest,
        (COLUMNAR_BATCH_HEADER_IDX_DICTIONARY_DESCRIPTOR_ID * 4) as usize,
        COLUMNAR_DESCRIPTOR_ID_NONE,
    );
    put_u32(dest, (COLUMNAR_BATCH_HEADER_IDX_RESERVED * 4) as usize, 0);

    for (i, f) in fields.iter().enumerate() {
        let base =
            (COLUMNAR_BATCH_HEADER_BYTES + (i as u32) * COLUMNAR_FIELD_DESCRIPTOR_BYTES) as usize;
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_FIELD_ID * 4) as usize, f.field_id);
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_LOGICAL_TYPE * 4) as usize, f.logical_type);
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_PHYSICAL_TYPE * 4) as usize, f.physical_type);
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_FLAGS * 4) as usize, f.flags);
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_LENGTH * 4) as usize, f.length);
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_NULL_COUNT * 4) as usize, 0);
        put_u32(
            dest,
            base + (COLUMNAR_FIELD_IDX_VALIDITY_DESCRIPTOR_ID * 4) as usize,
            COLUMNAR_DESCRIPTOR_ID_NONE,
        );
        put_u32(
            dest,
            base + (COLUMNAR_FIELD_IDX_OFFSETS_DESCRIPTOR_ID * 4) as usize,
            COLUMNAR_DESCRIPTOR_ID_NONE,
        );
        put_u32(
            dest,
            base + (COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID * 4) as usize,
            f.values_descriptor_id,
        );
        put_u32(
            dest,
            base + (COLUMNAR_FIELD_IDX_AUX_DESCRIPTOR_ID * 4) as usize,
            COLUMNAR_DESCRIPTOR_ID_NONE,
        );
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_BYTE_WIDTH * 4) as usize, f.byte_width);
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_SCALE * 4) as usize, 0);
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_PRECISION * 4) as usize, 0);
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_TIMEZONE_HASH * 4) as usize, 0);
        put_u32(
            dest,
            base + (COLUMNAR_FIELD_IDX_DICTIONARY_ID * 4) as usize,
            COLUMNAR_DESCRIPTOR_ID_NONE,
        );
        put_u32(dest, base + (COLUMNAR_FIELD_IDX_RESERVED * 4) as usize, 0);
    }

    Ok(total_bytes)
}

/// Carves and assembles a columnar batch header directly from a ScratchArena.
pub fn assemble_columnar_batch_scratch<'a>(
    row_count: u32,
    fields: &[ColumnarFieldSpec],
    arena: &'a mut ScratchArena,
) -> Result<&'a mut [u8], String> {
    let total_bytes = columnar_descriptor_payload_bytes(fields.len() as u32) as usize;
    let slab = arena.alloc_bytes(total_bytes).map_err(|error| error.to_string())?;
    assemble_columnar_batch_into(row_count, fields, slab)?;
    Ok(slab)
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

    #[test]
    fn test_assemble_columnar_batch() {
        let fields = [
            ColumnarFieldSpec::new_fixed(101, COLUMNAR_LOGICAL_TYPE_FLOAT, 100, 4, 1),
            ColumnarFieldSpec::new_fixed(102, COLUMNAR_LOGICAL_TYPE_UINT, 100, 4, 2),
        ];

        let mut dest = [0u8; 256];
        let bytes_written =
            assemble_columnar_batch_into(100, &fields, &mut dest).expect("assemble into");
        assert_eq!(bytes_written, 192);

        // Verify magic and counts
        let magic = u32::from_le_bytes(dest[0..4].try_into().expect("4-byte header field"));
        assert_eq!(magic, COLUMNAR_BATCH_MAGIC);
        let row_count = u32::from_le_bytes(dest[8..12].try_into().expect("4-byte header field"));
        assert_eq!(row_count, 100);
        let col_count = u32::from_le_bytes(dest[12..16].try_into().expect("4-byte header field"));
        assert_eq!(col_count, 2);

        // Verify field 0
        let f0_base = 32;
        let f0_id =
            u32::from_le_bytes(dest[f0_base..f0_base + 4].try_into().expect("4-byte header field"));
        assert_eq!(f0_id, 101);
        let f0_val_id = u32::from_le_bytes(
            dest[f0_base + 32..f0_base + 36].try_into().expect("4-byte header field"),
        );
        assert_eq!(f0_val_id, 1);

        // Test scratch assembly
        let mut arena = ScratchArena::with_capacity(512);
        let slab =
            assemble_columnar_batch_scratch(100, &fields, &mut arena).expect("scratch assemble");
        assert_eq!(slab.len(), 192);
        assert_eq!(&slab[..192], &dest[..192]);
    }
}
