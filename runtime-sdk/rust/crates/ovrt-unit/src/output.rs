//! Output ownership is selected by the caller before unit execution.

/// A checked result destination. Failed writes must return an error without truncation.
pub trait RuntimeOutput {
    fn write(&mut self, bytes: &[u8]) -> Result<(), String>;

    /// Transfers ownership when the destination supports it.
    fn write_owned(&mut self, bytes: Vec<u8>) -> Result<(), String> {
        self.write(&bytes)
    }
}

impl RuntimeOutput for Vec<u8> {
    fn write(&mut self, bytes: &[u8]) -> Result<(), String> {
        self.try_reserve(bytes.len()).map_err(|error| error.to_string())?;
        self.extend_from_slice(bytes);
        Ok(())
    }

    fn write_owned(&mut self, bytes: Vec<u8>) -> Result<(), String> {
        if self.is_empty() {
            *self = bytes;
            Ok(())
        } else {
            self.write(&bytes)
        }
    }
}

/// Writes into an exclusively borrowed region and tracks its initialized prefix.
pub struct SliceOutput<'a> {
    bytes: &'a mut [u8],
    written: usize,
}

impl<'a> SliceOutput<'a> {
    pub fn new(bytes: &'a mut [u8]) -> Self {
        Self { bytes, written: 0 }
    }

    pub fn written(&self) -> usize {
        self.written
    }
}

impl RuntimeOutput for SliceOutput<'_> {
    fn write(&mut self, bytes: &[u8]) -> Result<(), String> {
        let remaining = &mut self.bytes[self.written..];
        if bytes.len() > remaining.len() {
            return Err(format!(
                "output requires {} bytes; {} remain",
                bytes.len(),
                remaining.len()
            ));
        }
        remaining[..bytes.len()].copy_from_slice(bytes);
        self.written += bytes.len();
        Ok(())
    }
}

/// ScratchOutput writes into a ScratchArena without heap reallocation.
pub struct ScratchOutput<'a> {
    arena: &'a mut ovrt_core::ScratchArena,
    written: usize,
}

impl<'a> ScratchOutput<'a> {
    pub fn new(arena: &'a mut ovrt_core::ScratchArena) -> Self {
        let _ = arena.remaining_slice_mut();
        Self { arena, written: 0 }
    }

    pub fn written(&self) -> usize {
        self.written
    }

    /// Commits the written bytes into the arena and returns the slice.
    pub fn finish(self) -> Result<&'a [u8], String> {
        if self.written == 0 {
            return Ok(&[]);
        }
        let written = self.written;
        self.arena.commit(written).map(|slice| &*slice).map_err(|error| error.to_string())
    }
}

impl<'a> RuntimeOutput for ScratchOutput<'a> {
    fn write(&mut self, bytes: &[u8]) -> Result<(), String> {
        if bytes.is_empty() {
            return Ok(());
        }
        let remaining = self.arena.remaining_slice_mut();
        let target_end = self
            .written
            .checked_add(bytes.len())
            .ok_or_else(|| "output length overflow".to_string())?;
        if target_end > remaining.len() {
            return Err(format!(
                "output requires {} bytes; {} remain in scratch arena",
                target_end,
                remaining.len()
            ));
        }
        remaining[self.written..target_end].copy_from_slice(bytes);
        self.written = target_end;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bounded_output_preserves_adjacent_bytes_and_refuses_partial_writes() {
        let mut bytes = [99; 8];
        let mut output = SliceOutput::new(&mut bytes[2..6]);
        output.write(b"ab").expect("first chunk");
        assert!(output.write(b"cde").is_err());
        assert_eq!(output.written(), 2);
        output.write_owned(b"cd".to_vec()).expect("second chunk");
        assert_eq!(output.written(), 4);
        assert_eq!(bytes, [99, 99, b'a', b'b', b'c', b'd', 99, 99]);
    }

    #[test]
    fn owned_output_moves_allocations_and_appends_later_chunks() {
        let original = b"owned".to_vec();
        let address = original.as_ptr();
        let mut output = Vec::new();
        output.write_owned(original).expect("move");
        assert_eq!(output.as_ptr(), address);
        output.write_owned(b"-tail".to_vec()).expect("append");
        assert_eq!(output, b"owned-tail");
        SliceOutput::new(&mut []).write(b"").expect("empty write");
        assert!(SliceOutput::new(&mut []).write(b"x").is_err());
    }

    #[test]
    fn scratch_output_appends_chunks_and_commits_slice() {
        let mut arena = ovrt_core::ScratchArena::with_capacity(128);
        let mut output = ScratchOutput::new(&mut arena);
        output.write(b"hello ").expect("write first chunk");
        output.write(b"foundation").expect("write second chunk");
        assert_eq!(output.written(), 16);
        let committed = output.finish().expect("finish");
        assert_eq!(committed, b"hello foundation");
    }

    #[test]
    fn scratch_output_fails_on_capacity_exhaustion() {
        let mut arena = ovrt_core::ScratchArena::with_capacity(16);
        let mut output = ScratchOutput::new(&mut arena);
        let err = output.write(&[0u8; 32]);
        assert!(err.is_err());
    }
}
