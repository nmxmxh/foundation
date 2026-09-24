//! Generic arena adapter for units that take bytes and return bytes.
//!
//! Units receive an input slice and a checked output destination.
//! This adapter writes results directly into the output slab.
//! Units that construct owned results can transfer them through `write_owned`.
//! The control payload holds `INPUT_MAX_BYTES`, which is 1 KiB — sized
//! for control, not for workloads — so any unit whose real input is a ranking
//! batch, an embedding, or a document fails on every call in production while
//! passing its own tests on a small fixture.
//!
//! The fix is mechanical and identical every time: put the bytes in an arena
//! slab, send descriptor ids instead, write the result into a slab the host
//! pre-allocated. Writing that by hand once per unit produced four
//! near-identical copies across pronto alone, each with its own opportunity to
//! get the bounds check wrong. This adapter does it once.
//!
//! Wrap an existing unit and it gains an arena-backed identity, with the
//! original left in place for callers whose payloads genuinely fit:
//!
//! ```ignore
//! host.register_unit(Arc::new(MyUnit))?;                              // v1, control payload
//! host.register_unit(Arc::new(ArenaBlobUnit::new("my.unit.v2", MyUnit)))?;  // v2, arena
//! ```

use ovrt_core::{RuntimeRole, RuntimeUnitDescriptor};
use ovrt_unit::RuntimeUnit;

use crate::arena::Arena;

/// Envelope magic for a generic arena blob request: "ABLB".
pub const ARENA_BLOB_MAGIC: u32 = 0x4142_4C42;

/// Control payload: magic, input slab id, output slab id.
pub const ARENA_BLOB_REQUEST_BYTES: usize = 12;

fn read_u32(input: &[u8], at: usize) -> Result<u32, String> {
    if at + 4 > input.len() {
        return Err("arena blob request truncated".to_string());
    }
    Ok(u32::from_le_bytes([input[at], input[at + 1], input[at + 2], input[at + 3]]))
}

/// Decodes the control payload into (input slab, output slab).
pub fn decode_arena_blob_request(input: &[u8]) -> Result<(u32, u32), String> {
    if input.len() < ARENA_BLOB_REQUEST_BYTES {
        return Err(format!(
            "arena blob request is {} bytes, want {ARENA_BLOB_REQUEST_BYTES}",
            input.len()
        ));
    }
    let magic = read_u32(input, 0)?;
    if magic != ARENA_BLOB_MAGIC {
        return Err(format!("arena blob request magic {magic:#x}, want {ARENA_BLOB_MAGIC:#x}"));
    }
    Ok((read_u32(input, 4)?, read_u32(input, 8)?))
}

/// Runs a unit with arena input and a checked slab destination.
pub struct ArenaBlobUnit<U: RuntimeUnit> {
    unit_id: String,
    inner: U,
}

impl<U: RuntimeUnit> ArenaBlobUnit<U> {
    /// Wraps `inner` under a new unit id.
    ///
    /// A distinct id rather than replacing the original: the two differ in how
    /// the payload arrives, a host may have callers on both, and silently
    /// changing what an existing id expects would turn every un-migrated caller
    /// into a decode error.
    pub fn new(unit_id: impl Into<String>, inner: U) -> Self {
        Self { unit_id: unit_id.into(), inner }
    }
}

impl<U: RuntimeUnit> RuntimeUnit for ArenaBlobUnit<U> {
    fn descriptor(&self) -> RuntimeUnitDescriptor {
        let inner = self.inner.descriptor();
        RuntimeUnitDescriptor {
            unit_id: self.unit_id.clone(),
            role: RuntimeRole::Compute,
            input_schema: inner.input_schema,
            output_schema: inner.output_schema,
            // The arena is a native shared mapping; a browser host reaches its
            // arena a different way, so this identity is native-only.
            supports_wasm: false,
            supports_native: true,
            requires_shared_memory: true,
            supports_gpu: inner.supports_gpu,
            max_concurrency: inner.max_concurrency,
        }
    }

    fn execute(
        &self,
        input: &[u8],
        output: &mut dyn ovrt_unit::RuntimeOutput,
    ) -> Result<(), String> {
        let (input_slab, output_slab) = decode_arena_blob_request(input)?;
        let arena = Arena::global().ok_or_else(|| {
            "arena is not available; host did not provide OVRT_SHM_ARENA_PATH".to_string()
        })?;
        let mut destination = arena.output_for(input_slab, output_slab)?;
        self.inner.execute(arena.slab(input_slab)?, &mut destination)?;
        output.write(&(destination.written() as u32).to_le_bytes())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    struct Echo;

    impl RuntimeUnit for Echo {
        fn descriptor(&self) -> RuntimeUnitDescriptor {
            RuntimeUnitDescriptor {
                unit_id: "test.echo".to_string(),
                role: RuntimeRole::Compute,
                input_schema: "test/in".to_string(),
                output_schema: "test/out".to_string(),
                supports_wasm: true,
                supports_native: true,
                requires_shared_memory: false,
                supports_gpu: false,
                max_concurrency: 2,
            }
        }

        fn execute(
            &self,
            input: &[u8],
            __ovrt_output: &mut dyn ovrt_unit::RuntimeOutput,
        ) -> Result<(), String> {
            __ovrt_output.write(input)
        }
    }

    #[test]
    fn wrapping_takes_the_new_id_and_declares_shared_memory() {
        let unit = ArenaBlobUnit::new("test.echo.v2", Echo);
        let descriptor = unit.descriptor();
        assert_eq!(descriptor.unit_id, "test.echo.v2");
        assert!(descriptor.requires_shared_memory);
        assert!(!descriptor.supports_wasm);
        // Inherited from the wrapped unit rather than invented.
        assert_eq!(descriptor.max_concurrency, 2);
        assert_eq!(descriptor.input_schema, "test/in");
    }

    #[test]
    fn rejects_a_request_with_the_wrong_magic() {
        let mut payload = vec![0u8; ARENA_BLOB_REQUEST_BYTES];
        payload[0] = 9;
        assert!(decode_arena_blob_request(&payload).is_err());
    }

    #[test]
    fn rejects_a_truncated_request() {
        assert!(decode_arena_blob_request(&[0u8; 8]).is_err());
    }

    #[test]
    fn decodes_slab_ids_in_order() {
        let mut payload = Vec::new();
        payload.extend_from_slice(&ARENA_BLOB_MAGIC.to_le_bytes());
        payload.extend_from_slice(&7u32.to_le_bytes());
        payload.extend_from_slice(&11u32.to_le_bytes());
        assert_eq!(decode_arena_blob_request(&payload), Ok((7, 11)));
    }

    /// Without an arena the unit must fail, not fall through to reading the
    /// control payload as if it were the workload.
    #[test]
    fn fails_when_no_arena_is_configured() {
        if std::env::var(crate::arena::ARENA_PATH_ENV).is_ok() {
            return;
        }
        let unit = ArenaBlobUnit::new("test.echo.v2", Echo);
        let mut payload = Vec::new();
        payload.extend_from_slice(&ARENA_BLOB_MAGIC.to_le_bytes());
        payload.extend_from_slice(&0u32.to_le_bytes());
        payload.extend_from_slice(&1u32.to_le_bytes());
        assert!(unit.run(&payload).is_err());
    }
}
