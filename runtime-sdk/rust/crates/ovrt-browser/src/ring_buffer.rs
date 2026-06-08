use crate::buffer::SafeBuffer;

#[derive(Clone, Copy, Debug)]
pub struct RuntimeRingBuffer {
    buffer: SafeBuffer,
    base_offset: u32,
    capacity: u32,
}

impl RuntimeRingBuffer {
    const HEADER_BYTES: u32 = 8;
    const FRAME_HEADER_BYTES: u32 = 4;

    pub fn new(buffer: SafeBuffer, base_offset: u32, total_bytes: u32) -> Result<Self, String> {
        if total_bytes <= Self::HEADER_BYTES + Self::FRAME_HEADER_BYTES {
            return Err("runtime ring buffer is too small".to_string());
        }
        if base_offset.checked_add(total_bytes).is_none() {
            return Err("runtime ring buffer offset overflow".to_string());
        }
        Ok(Self { buffer, base_offset, capacity: total_bytes - Self::HEADER_BYTES })
    }

    pub fn available(&self) -> Result<u32, String> {
        let head = self.load_head()?;
        let tail = self.load_tail()?;
        Ok(self.distance(head, tail))
    }

    pub fn free_space(&self) -> Result<u32, String> {
        let head = self.load_head()?;
        let tail = self.load_tail()?;
        Ok(self.capacity.saturating_sub(self.distance(head, tail)).saturating_sub(1))
    }

    pub fn write_message(&self, payload: &[u8]) -> Result<bool, String> {
        let payload_len = u32::try_from(payload.len())
            .map_err(|_| "runtime ring payload too large".to_string())?;
        let frame_len = payload_len
            .checked_add(Self::FRAME_HEADER_BYTES)
            .ok_or_else(|| "runtime ring frame length overflow".to_string())?;
        if frame_len > self.capacity {
            return Err("runtime ring payload exceeds buffer capacity".to_string());
        }
        if self.free_space()? < frame_len {
            return Ok(false);
        }

        let tail = self.load_tail()?;
        let data_start = (tail + Self::FRAME_HEADER_BYTES) % self.capacity;
        self.write_raw_at(data_start, payload)?;
        self.write_raw_at(tail, &payload_len.to_le_bytes())?;
        self.store_tail((tail + frame_len) % self.capacity)?;
        Ok(true)
    }

    pub fn read_message(&self, target: &mut [u8]) -> Result<Option<usize>, String> {
        let head = self.load_head()?;
        let tail = self.load_tail()?;
        if head == tail {
            return Ok(None);
        }

        let mut length_bytes = [0u8; 4];
        self.read_raw_at(head, &mut length_bytes)?;
        let payload_len = u32::from_le_bytes(length_bytes);
        if payload_len == 0 {
            return Ok(None);
        }
        if payload_len > self.distance(head, tail).saturating_sub(Self::FRAME_HEADER_BYTES) {
            return Err("runtime ring frame length exceeds available bytes".to_string());
        }
        let payload_len_usize = payload_len as usize;
        if payload_len_usize > target.len() {
            return Err(format!(
                "runtime ring target too small: {} < {}",
                target.len(),
                payload_len_usize
            ));
        }

        let data_start = (head + Self::FRAME_HEADER_BYTES) % self.capacity;
        self.read_raw_at(data_start, &mut target[..payload_len_usize])?;
        self.write_raw_at(head, &[0, 0, 0, 0])?;
        self.store_head((head + Self::FRAME_HEADER_BYTES + payload_len) % self.capacity)?;
        Ok(Some(payload_len_usize))
    }

    fn load_head(&self) -> Result<u32, String> {
        let bytes = self.buffer.read_at(self.base_offset, 4)?;
        let array: [u8; 4] =
            bytes.try_into().map_err(|_| "runtime ring head must be 4 bytes".to_string())?;
        Ok(u32::from_le_bytes(array))
    }

    fn store_head(&self, value: u32) -> Result<(), String> {
        self.buffer.write_at(self.base_offset, &value.to_le_bytes())
    }

    fn load_tail(&self) -> Result<u32, String> {
        let bytes = self.buffer.read_at(self.base_offset + 4, 4)?;
        let array: [u8; 4] =
            bytes.try_into().map_err(|_| "runtime ring tail must be 4 bytes".to_string())?;
        Ok(u32::from_le_bytes(array))
    }

    fn store_tail(&self, value: u32) -> Result<(), String> {
        self.buffer.write_at(self.base_offset + 4, &value.to_le_bytes())
    }

    fn data_base(&self) -> u32 {
        self.base_offset + Self::HEADER_BYTES
    }

    fn distance(&self, head: u32, tail: u32) -> u32 {
        if tail >= head {
            tail - head
        } else {
            self.capacity - (head - tail)
        }
    }

    fn write_raw_at(&self, offset: u32, payload: &[u8]) -> Result<(), String> {
        let first_len = payload.len().min((self.capacity - offset) as usize);
        let second_len = payload.len() - first_len;
        self.buffer.write_at(self.data_base() + offset, &payload[..first_len])?;
        if second_len > 0 {
            self.buffer.write_at(self.data_base(), &payload[first_len..])?;
        }
        Ok(())
    }

    fn read_raw_at(&self, offset: u32, target: &mut [u8]) -> Result<(), String> {
        let first_len = target.len().min((self.capacity - offset) as usize);
        let second_len = target.len() - first_len;
        let first = self.buffer.read_at(self.data_base() + offset, first_len as u32)?;
        target[..first_len].copy_from_slice(&first);
        if second_len > 0 {
            let second = self.buffer.read_at(self.data_base(), second_len as u32)?;
            target[first_len..].copy_from_slice(&second);
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use ovrt_core::BUFFER_TOTAL_BYTES;

    use super::*;

    #[test]
    fn round_trips_messages() {
        let handle = crate::js_interop::create_mock_buffer(BUFFER_TOTAL_BYTES as usize);
        let buffer = SafeBuffer::new(handle).expect("safe buffer");
        let ring = RuntimeRingBuffer::new(buffer, 1024, 128).expect("ring");

        assert!(ring.write_message(b"hello").expect("write"));
        let mut target = [0u8; 16];
        let len = ring.read_message(&mut target).expect("read").expect("message");
        assert_eq!(&target[..len], b"hello");
        assert!(ring.read_message(&mut target).expect("read empty").is_none());
    }

    #[test]
    fn wraps_around_buffer_end() {
        let handle = crate::js_interop::create_mock_buffer(BUFFER_TOTAL_BYTES as usize);
        let buffer = SafeBuffer::new(handle).expect("safe buffer");
        let ring = RuntimeRingBuffer::new(buffer, 2048, 32).expect("ring");

        assert!(ring.write_message(b"aaaa").expect("write a"));
        assert!(ring.write_message(b"bbbb").expect("write b"));
        let mut target = [0u8; 8];
        assert_eq!(ring.read_message(&mut target).expect("read a"), Some(4));
        assert!(ring.write_message(b"cccc").expect("write c"));
        assert_eq!(ring.read_message(&mut target).expect("read b"), Some(4));
        assert_eq!(&target[..4], b"bbbb");
        assert_eq!(ring.read_message(&mut target).expect("read c"), Some(4));
        assert_eq!(&target[..4], b"cccc");
    }

    #[test]
    fn rejects_oversized_messages() {
        let handle = crate::js_interop::create_mock_buffer(BUFFER_TOTAL_BYTES as usize);
        let buffer = SafeBuffer::new(handle).expect("safe buffer");
        let ring = RuntimeRingBuffer::new(buffer, 4096, 32).expect("ring");
        assert!(ring.write_message(&[0u8; 64]).is_err());
    }
}
