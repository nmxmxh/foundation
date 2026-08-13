//! Consumer side of the shared arena data plane.
//!
//! The 4 KiB control buffer carries control: a schema version, a status code, a
//! few epochs, and at most 1 KiB of payload. That is the right size for a
//! control message and the wrong size for a workload, and conflating the two is
//! what forced units handed real data — an embedding matrix, a ranking batch —
//! to fail every exchange with "input payload too large" while their callers
//! silently took a fallback path.
//!
//! `runtime_shared_arena.capnp` specifies the answer and the browser host
//! already implements it: a separately mapped region of page-aligned slabs
//! addressed by a descriptor table. The host writes bulk data there and puts a
//! descriptor id in the control payload; a unit reads the slab by id. Four bytes
//! cross the control plane no matter how many megabytes the batch holds.
//!
//! Implementation note: this maps the region. It used to read it with
//! positional file I/O, on the reasoning that `ovrt-native` is
//! `#![forbid(unsafe_code)]` and one read per exchange is a memcpy against work
//! superlinear in the batch. Two things were wrong with that. The copy was not
//! one per exchange — `descriptor` alone issued four four-byte `pread`s, and
//! reading a columnar batch cost a syscall and a heap allocation per column.
//! And a positional reader against the host's mapped writer is the coherence
//! mismatch that forced the host to unmap its own arena and publish through a
//! staging buffer. `ovrt_core::SharedMapping` contains the unsafe in a crate
//! that permits it, so this crate still forbids it and the mismatch is gone:
//! a slab is a borrow of memory the host already wrote.

use std::env;
use std::sync::OnceLock;

use ovrt_core::SharedMapping;

use ovrt_core::generated::{
    ARENA_DESCRIPTOR_COUNT, ARENA_DESCRIPTOR_SIZE, ARENA_DESCRIPTOR_STATE_READY,
    ARENA_HEADER_IDX_CAPACITY_BYTES, ARENA_HEADER_IDX_MAGIC, ARENA_HEADER_IDX_SCHEMA_VERSION,
    ARENA_HEADER_MAGIC, ARENA_OFFSET_DESCRIPTOR_TABLE, ARENA_OFFSET_HEADER, ARENA_OFFSET_PAGES,
    ARENA_SCHEMA_VERSION, COLUMNAR_BATCH_HEADER_BYTES, COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT,
    COLUMNAR_BATCH_HEADER_IDX_MAGIC, COLUMNAR_BATCH_HEADER_IDX_ROW_COUNT, COLUMNAR_BATCH_MAGIC,
    COLUMNAR_FIELD_DESCRIPTOR_BYTES, COLUMNAR_FIELD_IDX_BYTE_WIDTH, COLUMNAR_FIELD_IDX_FIELD_ID,
    COLUMNAR_FIELD_IDX_LENGTH, COLUMNAR_FIELD_IDX_LOGICAL_TYPE,
    COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID, COLUMNAR_LOGICAL_TYPE_FLOAT,
    COLUMNAR_LOGICAL_TYPE_UINT,
};

/// Environment variable naming the arena mapping. Set by the host alongside
/// `OVRT_SHM_PATH`; absent when the pool runs without a data plane.
pub const ARENA_PATH_ENV: &str = "OVRT_SHM_ARENA_PATH";

/// Descriptor field offsets within a 32-byte table entry, little-endian.
/// Mirrors the Go host and `ts/browser-host/src/arena.ts`.
const FIELD_STATE: u64 = 0;
const FIELD_OFFSET: u64 = 4;
const FIELD_LENGTH: u64 = 8;
const FIELD_TYPE: u64 = 16;

/// One slab's addressing information.
#[derive(Debug, Clone, Copy)]
pub struct ArenaDescriptor {
    pub id: u32,
    pub state: u32,
    pub offset: u32,
    pub length: u32,
    pub kind: u32,
}

/// A fixed-width column in a columnar batch.
#[derive(Debug, Clone, Copy)]
pub struct ColumnarField {
    pub field_id: u32,
    pub logical_type: u32,
    pub length: u32,
    pub byte_width: u32,
    pub values_descriptor_id: u32,
}

/// A decoded batch header plus its field table.
#[derive(Debug, Clone)]
pub struct ColumnarBatch {
    pub row_count: u32,
    pub fields: Vec<ColumnarField>,
}

impl ColumnarBatch {
    /// Look up a column by the id the producer assigned it.
    ///
    /// By id rather than position: a producer may add a column without
    /// renumbering the rest, and a consumer that indexed positionally would then
    /// read the wrong column with no error.
    pub fn field(&self, field_id: u32) -> Option<&ColumnarField> {
        self.fields.iter().find(|field| field.field_id == field_id)
    }
}

/// A view of the arena the host mapped.
pub struct Arena {
    mapping: SharedMapping,
    capacity: u32,
}

static GLOBAL: OnceLock<Option<Arena>> = OnceLock::new();

impl Arena {
    /// Opens the process-wide arena, or `None` when the host did not provide one.
    ///
    /// Cached: the mapping is per process and immutable in identity, so a unit
    /// invoked thousands of times must not reopen it thousands of times.
    pub fn global() -> Option<&'static Arena> {
        GLOBAL
            .get_or_init(|| match env::var(ARENA_PATH_ENV) {
                Ok(path) if !path.trim().is_empty() => match Arena::open(path.trim()) {
                    Ok(arena) => Some(arena),
                    Err(error) => {
                        // Loud, and only once: a host that provided a path and
                        // an arena that cannot be opened is a wiring fault, and
                        // swallowing it here reports the arena as merely absent
                        // — which sends every reader looking for a missing
                        // environment variable that is in fact present.
                        eprintln!("ovrt-native: arena at {path} is unusable: {error}");
                        None
                    }
                },
                _ => None,
            })
            .as_ref()
    }

    /// Opens an arena at `path`, validating the header before use.
    pub fn open(path: &str) -> Result<Arena, String> {
        // Mapped read/write. A unit consumes an input batch and returns a result
        // that is itself too large for the control buffer's 2 KiB output region,
        // so the host pre-allocates an output slab and the unit fills it. The
        // unit never allocates: Go owns the allocator, which keeps the bump
        // pointer single-writer and means a kernel crash cannot corrupt it.
        //
        // The size comes from the file, not from the header, because the header
        // cannot be read until something is mapped. The host sizes the file
        // before it spawns this process, so there is nothing to race with.
        let bytes =
            std::fs::metadata(path).map_err(|error| format!("stat arena {path}: {error}"))?.len();
        let bytes = usize::try_from(bytes)
            .map_err(|_| format!("arena {path} is {bytes} bytes, too large to map"))?;
        let mapping = SharedMapping::open(std::path::Path::new(path), bytes)?;
        let arena = Arena { mapping, capacity: 0 };

        let magic = arena.header_u32(ARENA_HEADER_IDX_MAGIC)?;
        if magic != ARENA_HEADER_MAGIC {
            return Err(format!(
                "arena magic {magic:#x}, want {ARENA_HEADER_MAGIC:#x}; the mapping is not an arena"
            ));
        }
        let version = arena.header_u32(ARENA_HEADER_IDX_SCHEMA_VERSION)?;
        if version != ARENA_SCHEMA_VERSION {
            return Err(format!(
                "arena schema version {version}, this build understands {ARENA_SCHEMA_VERSION}"
            ));
        }
        let capacity = arena.header_u32(ARENA_HEADER_IDX_CAPACITY_BYTES)?;
        Ok(Arena { capacity, ..arena })
    }

    /// Borrows a region of the mapping, bounds-checked against it.
    ///
    /// A borrow rather than a copy: this is the whole reason the arena exists,
    /// and it is what the previous `pread`-into-a-`Vec` gave up on every call.
    fn bytes_at(&self, offset: u64, len: usize) -> Result<&[u8], String> {
        let start = usize::try_from(offset)
            .map_err(|_| format!("arena offset {offset} does not fit an address"))?;
        let end = start
            .checked_add(len)
            .ok_or_else(|| format!("arena region overflow at {offset}+{len}"))?;
        let raw = self.mapping.as_slice();
        if end > raw.len() {
            return Err(format!(
                "arena region [{start}, {end}) runs past the {}-byte mapping",
                raw.len()
            ));
        }
        Ok(&raw[start..end])
    }

    fn u32_at(&self, offset: u64) -> Result<u32, String> {
        let bytes = self.bytes_at(offset, 4)?;
        Ok(u32::from_le_bytes([bytes[0], bytes[1], bytes[2], bytes[3]]))
    }

    fn header_u32(&self, index: u32) -> Result<u32, String> {
        self.u32_at(u64::from(ARENA_OFFSET_HEADER) + u64::from(index) * 4)
    }

    /// Reads one descriptor table entry.
    pub fn descriptor(&self, id: u32) -> Result<ArenaDescriptor, String> {
        if id >= ARENA_DESCRIPTOR_COUNT {
            return Err(format!("arena descriptor id {id} out of range"));
        }
        let base = u64::from(ARENA_OFFSET_DESCRIPTOR_TABLE)
            + u64::from(id) * u64::from(ARENA_DESCRIPTOR_SIZE);
        Ok(ArenaDescriptor {
            id,
            state: self.u32_at(base + FIELD_STATE)?,
            offset: self.u32_at(base + FIELD_OFFSET)?,
            length: self.u32_at(base + FIELD_LENGTH)?,
            kind: self.u32_at(base + FIELD_TYPE)?,
        })
    }

    /// Returns the bytes a descriptor addresses.
    ///
    /// Bounds are taken from the table and validated against the arena's own
    /// capacity. The region is written by another process, so a descriptor is
    /// untrusted input: a corrupt or stale entry must produce an error, not a
    /// read outside the slab region.
    pub fn slab(&self, id: u32) -> Result<&[u8], String> {
        let descriptor = self.descriptor(id)?;
        if descriptor.state != ARENA_DESCRIPTOR_STATE_READY {
            return Err(format!(
                "arena descriptor {id} is in state {}, not READY; the producer has not published it",
                descriptor.state
            ));
        }
        if descriptor.offset < ARENA_OFFSET_PAGES {
            return Err(format!(
                "arena descriptor {id} points at {} inside the control region",
                descriptor.offset
            ));
        }
        let end = u64::from(descriptor.offset) + u64::from(descriptor.length);
        if self.capacity > 0 && end > u64::from(self.capacity) {
            return Err(format!(
                "arena descriptor {id} addresses [{}, {end}) past the {} byte arena",
                descriptor.offset, self.capacity
            ));
        }
        self.bytes_at(u64::from(descriptor.offset), descriptor.length as usize)
    }

    /// Decodes a columnar batch header and its field table.
    pub fn columnar_batch(&self, batch_descriptor_id: u32) -> Result<ColumnarBatch, String> {
        let slab = self.slab(batch_descriptor_id)?;
        if slab.len() < COLUMNAR_BATCH_HEADER_BYTES as usize {
            return Err(format!(
                "columnar batch slab is {} bytes, want at least {COLUMNAR_BATCH_HEADER_BYTES}",
                slab.len()
            ));
        }
        let word = |index: u32| -> u32 {
            let at = index as usize * 4;
            u32::from_le_bytes([slab[at], slab[at + 1], slab[at + 2], slab[at + 3]])
        };
        let magic = word(COLUMNAR_BATCH_HEADER_IDX_MAGIC);
        if magic != COLUMNAR_BATCH_MAGIC {
            return Err(format!("columnar batch magic {magic:#x}, want {COLUMNAR_BATCH_MAGIC:#x}"));
        }
        let row_count = word(COLUMNAR_BATCH_HEADER_IDX_ROW_COUNT);
        let column_count = word(COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT);

        let need = COLUMNAR_BATCH_HEADER_BYTES as usize
            + column_count as usize * COLUMNAR_FIELD_DESCRIPTOR_BYTES as usize;
        if slab.len() < need {
            return Err(format!(
                "columnar batch slab is {} bytes, want {need} for {column_count} columns",
                slab.len()
            ));
        }

        let mut fields = Vec::with_capacity(column_count as usize);
        for column in 0..column_count {
            let base = COLUMNAR_BATCH_HEADER_BYTES as usize
                + column as usize * COLUMNAR_FIELD_DESCRIPTOR_BYTES as usize;
            let field_word = |index: u32| -> u32 {
                let at = base + index as usize * 4;
                u32::from_le_bytes([slab[at], slab[at + 1], slab[at + 2], slab[at + 3]])
            };
            fields.push(ColumnarField {
                field_id: field_word(COLUMNAR_FIELD_IDX_FIELD_ID),
                logical_type: field_word(COLUMNAR_FIELD_IDX_LOGICAL_TYPE),
                length: field_word(COLUMNAR_FIELD_IDX_LENGTH),
                byte_width: field_word(COLUMNAR_FIELD_IDX_BYTE_WIDTH),
                values_descriptor_id: field_word(COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID),
            });
        }
        Ok(ColumnarBatch { row_count, fields })
    }

    /// Reads a float32 column's values.
    pub fn f32_column(&self, field: &ColumnarField) -> Result<Vec<f32>, String> {
        if field.logical_type != COLUMNAR_LOGICAL_TYPE_FLOAT || field.byte_width != 4 {
            return Err(format!(
                "column {} is logical type {} width {}, not float32",
                field.field_id, field.logical_type, field.byte_width
            ));
        }
        let slab = self.slab(field.values_descriptor_id)?;
        let want = field.length as usize * 4;
        if slab.len() < want {
            return Err(format!(
                "float32 column {} slab is {} bytes, want {want}",
                field.field_id,
                slab.len()
            ));
        }
        Ok(slab[..want]
            .chunks_exact(4)
            .map(|chunk| f32::from_le_bytes([chunk[0], chunk[1], chunk[2], chunk[3]]))
            .collect())
    }

    /// Reads a uint32 column's values.
    pub fn u32_column(&self, field: &ColumnarField) -> Result<Vec<u32>, String> {
        if field.logical_type != COLUMNAR_LOGICAL_TYPE_UINT || field.byte_width != 4 {
            return Err(format!(
                "column {} is logical type {} width {}, not uint32",
                field.field_id, field.logical_type, field.byte_width
            ));
        }
        let slab = self.slab(field.values_descriptor_id)?;
        let want = field.length as usize * 4;
        if slab.len() < want {
            return Err(format!(
                "uint32 column {} slab is {} bytes, want {want}",
                field.field_id,
                slab.len()
            ));
        }
        Ok(slab[..want]
            .chunks_exact(4)
            .map(|chunk| u32::from_le_bytes([chunk[0], chunk[1], chunk[2], chunk[3]]))
            .collect())
    }
}

impl Arena {
    /// Writes bytes into a slab the host pre-allocated.
    ///
    /// Bounds come from the descriptor table, not from the caller, and the write
    /// is refused if it would exceed the slab. The unit is filling memory another
    /// process owns, so overrunning a slab would silently corrupt an unrelated
    /// batch rather than fail.
    pub fn write_slab(&self, id: u32, payload: &[u8]) -> Result<(), String> {
        let descriptor = self.descriptor(id)?;
        if descriptor.offset < ARENA_OFFSET_PAGES {
            return Err(format!(
                "arena descriptor {id} points at {} inside the control region",
                descriptor.offset
            ));
        }
        if payload.len() > descriptor.length as usize {
            return Err(format!(
                "result is {} bytes, output slab {id} holds {}",
                payload.len(),
                descriptor.length
            ));
        }
        let end = u64::from(descriptor.offset) + payload.len() as u64;
        if self.capacity > 0 && end > u64::from(self.capacity) {
            return Err(format!(
                "output slab {id} write would pass the {} byte arena",
                self.capacity
            ));
        }
        let offset = usize::try_from(descriptor.offset)
            .map_err(|_| format!("arena descriptor {id} offset does not fit an address"))?;
        self.mapping.write_at(offset, payload)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    /// Builds an arena byte-for-byte the way the Go host writes one.
    ///
    /// Hand-rolled rather than generated so the test fails if either side drifts
    /// from the capnp layout: a shared binary contract is only a contract if both
    /// ends are pinned to it independently.
    fn write_test_arena(values: &[f32]) -> (tempfile::NamedTempFile, u32) {
        let capacity = ovrt_core::generated::ARENA_MIN_BYTES;
        let mut raw = vec![0_u8; capacity as usize];

        let put = |raw: &mut Vec<u8>, at: usize, v: u32| {
            raw[at..at + 4].copy_from_slice(&v.to_le_bytes());
        };
        let header = |idx: u32| (ARENA_OFFSET_HEADER + idx * 4) as usize;
        put(&mut raw, header(ARENA_HEADER_IDX_MAGIC), ARENA_HEADER_MAGIC);
        put(&mut raw, header(ARENA_HEADER_IDX_SCHEMA_VERSION), ARENA_SCHEMA_VERSION);
        put(&mut raw, header(ARENA_HEADER_IDX_CAPACITY_BYTES), capacity);

        // Descriptor 0: the float column values. Descriptor 1: the batch header.
        let values_offset = ARENA_OFFSET_PAGES;
        let values_len = (values.len() * 4) as u32;
        // Page-align past the values. A fixed gap silently overlapped the two
        // slabs once the column grew past it, which is exactly the corruption
        // the real allocator's page alignment prevents.
        let page = ovrt_core::generated::ARENA_PAGE_BYTES;
        let batch_offset = values_offset + values_len.div_ceil(page) * page;
        let batch_len = COLUMNAR_BATCH_HEADER_BYTES + COLUMNAR_FIELD_DESCRIPTOR_BYTES;

        let put_descriptor = |raw: &mut Vec<u8>, id: u32, offset: u32, len: u32, kind: u32| {
            let base = (ARENA_OFFSET_DESCRIPTOR_TABLE + id * ARENA_DESCRIPTOR_SIZE) as usize;
            put(raw, base + FIELD_STATE as usize, ARENA_DESCRIPTOR_STATE_READY);
            put(raw, base + FIELD_OFFSET as usize, offset);
            put(raw, base + FIELD_LENGTH as usize, len);
            put(raw, base + FIELD_TYPE as usize, kind);
        };
        put_descriptor(&mut raw, 0, values_offset, values_len, 7);
        put_descriptor(&mut raw, 1, batch_offset, batch_len, 5);

        for (i, value) in values.iter().enumerate() {
            let at = values_offset as usize + i * 4;
            raw[at..at + 4].copy_from_slice(&value.to_le_bytes());
        }

        let b = batch_offset as usize;
        put(&mut raw, b + (COLUMNAR_BATCH_HEADER_IDX_MAGIC * 4) as usize, COLUMNAR_BATCH_MAGIC);
        put(&mut raw, b + (COLUMNAR_BATCH_HEADER_IDX_ROW_COUNT * 4) as usize, values.len() as u32);
        put(&mut raw, b + (COLUMNAR_BATCH_HEADER_IDX_COLUMN_COUNT * 4) as usize, 1);
        let f = b + COLUMNAR_BATCH_HEADER_BYTES as usize;
        put(&mut raw, f + (COLUMNAR_FIELD_IDX_FIELD_ID * 4) as usize, 0);
        put(
            &mut raw,
            f + (COLUMNAR_FIELD_IDX_LOGICAL_TYPE * 4) as usize,
            COLUMNAR_LOGICAL_TYPE_FLOAT,
        );
        put(&mut raw, f + (COLUMNAR_FIELD_IDX_LENGTH * 4) as usize, values.len() as u32);
        put(&mut raw, f + (COLUMNAR_FIELD_IDX_BYTE_WIDTH * 4) as usize, 4);
        put(&mut raw, f + (COLUMNAR_FIELD_IDX_VALUES_DESCRIPTOR_ID * 4) as usize, 0);

        let mut file = tempfile::NamedTempFile::new().expect("temp arena");
        file.write_all(&raw).expect("write arena");
        file.flush().expect("flush arena");
        (file, 1)
    }

    #[test]
    fn reads_a_columnar_batch_larger_than_the_control_buffer() {
        // 4000 floats is 16 KB — sixteen times the control payload ceiling that
        // this data plane exists to get past.
        let values: Vec<f32> = (0..4000).map(|i| i as f32 * 0.5).collect();
        assert!(values.len() * 4 > ovrt_core::generated::INPUT_MAX_BYTES as usize);

        let (file, batch_id) = write_test_arena(&values);
        let arena = Arena::open(file.path().to_str().expect("temp path is valid utf-8"))
            .expect("open arena");

        let batch = arena.columnar_batch(batch_id).expect("decode batch");
        assert_eq!(batch.row_count, values.len() as u32);
        assert_eq!(batch.fields.len(), 1);

        let field = batch.field(0).expect("field 0 present");
        let back = arena.f32_column(field).expect("read column");
        assert_eq!(back, values);
    }

    #[test]
    fn rejects_a_mapping_that_is_not_an_arena() {
        let mut file = tempfile::NamedTempFile::new().expect("temp");
        file.write_all(&vec![0_u8; 8192]).expect("write");
        file.flush().expect("flush");
        assert!(Arena::open(file.path().to_str().expect("temp path is valid utf-8")).is_err());
    }

    #[test]
    fn rejects_a_descriptor_pointing_into_the_control_region() {
        let values: Vec<f32> = vec![1.0, 2.0, 3.0];
        let (file, _) = write_test_arena(&values);
        let arena = Arena::open(file.path().to_str().expect("temp path is valid utf-8"))
            .expect("open arena");

        // Descriptor 5 was never written, so it is FREE — a consumer must refuse
        // it rather than read whatever bytes lie at offset zero.
        assert!(arena.slab(5).is_err());
    }

    /// The property the transport now rests on, stated as a test.
    ///
    /// A result this side writes must be visible to a reader holding its own
    /// mapping of the same arena, with no flush and no second copy. Under the
    /// positional design it was not: the host's established mapping never saw a
    /// `pwrite`, which is why the host unmapped its arena and read results back
    /// through the file. If this fails, that whole apparatus has to come back.
    #[test]
    fn a_written_slab_is_visible_through_another_mapping_of_the_arena() {
        let values: Vec<f32> = vec![1.0, 2.0, 3.0];
        let (file, _) = write_test_arena(&values);
        let path = file.path().to_str().expect("temp path is valid utf-8");

        let kernel = Arena::open(path).expect("open kernel arena");
        let host = Arena::open(path).expect("open host arena");

        kernel.write_slab(0, &[0xDE, 0xAD, 0xBE, 0xEF]).expect("write slab");

        let observed = host.slab(0).expect("read slab");
        assert_eq!(&observed[..4], &[0xDE, 0xAD, 0xBE, 0xEF]);
    }

    /// A write that would overrun its slab must fail, not spill into the next.
    #[test]
    fn a_write_larger_than_its_slab_is_refused() {
        let values: Vec<f32> = vec![1.0, 2.0, 3.0];
        let (file, _) = write_test_arena(&values);
        let arena = Arena::open(file.path().to_str().expect("temp path is valid utf-8"))
            .expect("open arena");

        // Descriptor 0 holds 3 f32s: 12 bytes.
        let err = arena.write_slab(0, &[0_u8; 13]).expect_err("oversized write must fail");
        assert!(err.contains("output slab 0 holds 12"), "unexpected error: {err}");
    }
}
