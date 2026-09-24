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
}
