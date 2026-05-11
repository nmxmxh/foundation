use std::io::{self, BufReader, BufWriter, Read, Write};
use std::panic::{catch_unwind, AssertUnwindSafe};

use ovrt_core::{
    BUFFER_TOTAL_BYTES, DIAGNOSTIC_MAX_BYTES, EPOCH_SLOT_BYTES, EPOCH_SLOT_COUNT, HEADER_INT_COUNT,
    IDX_DIAGNOSTICS_WRITTEN, IDX_OUTPUT_WRITTEN, IDX_PANIC_STATE, IDX_RUNTIME_TICK,
    INPUT_MAX_BYTES, INT_IDX_INPUT_LENGTH, INT_IDX_OUTPUT_LENGTH, INT_IDX_STATUS_CODE,
    OFFSET_DIAGNOSTIC_BYTES, OFFSET_EPOCHS, OFFSET_HEADER_INTS, OFFSET_INPUT_BYTES,
    OFFSET_OUTPUT_BYTES, OUTPUT_MAX_BYTES,
};

use crate::{panic_payload_message, NativeRuntimeHost};

pub fn serve_stdio(host: &NativeRuntimeHost) -> Result<(), String> {
    let stdin = io::stdin();
    let stdout = io::stdout();
    let mut reader = BufReader::new(stdin.lock());
    let mut writer = BufWriter::new(stdout.lock());
    serve_framed_session(host, &mut reader, &mut writer)
}

pub fn serve_framed_session<R: Read, W: Write>(
    host: &NativeRuntimeHost,
    reader: &mut R,
    writer: &mut W,
) -> Result<(), String> {
    loop {
        let unit_id_bytes = match read_frame(reader) {
            Ok(payload) => payload,
            Err(FrameReadError::EndOfStream) => return Ok(()),
            Err(FrameReadError::Io(error)) => return Err(format!("read unit frame: {error}")),
        };
        let unit_id = String::from_utf8(unit_id_bytes).map_err(|error| error.to_string())?;

        let raw_buffer = match read_frame(reader) {
            Ok(payload) => payload,
            Err(FrameReadError::EndOfStream) => {
                return Err("received unit frame without a matching runtime buffer".to_string())
            }
            Err(FrameReadError::Io(error)) => return Err(format!("read runtime buffer: {error}")),
        };

        let output = process_runtime_buffer(host, &unit_id, raw_buffer)?;
        write_frame(writer, &output).map_err(|error| format!("write runtime buffer: {error}"))?;
        writer.flush().map_err(|error| error.to_string())?;
    }
}

pub fn process_runtime_buffer(
    host: &NativeRuntimeHost,
    unit_id: &str,
    raw_buffer: Vec<u8>,
) -> Result<Vec<u8>, String> {
    let mut owned = raw_buffer;
    process_runtime_buffer_in_place(host, unit_id, &mut owned)?;
    Ok(owned)
}

pub fn process_runtime_buffer_in_place(
    host: &NativeRuntimeHost,
    unit_id: &str,
    raw_buffer: &mut [u8],
) -> Result<(), String> {
    if raw_buffer.len() != BUFFER_TOTAL_BYTES as usize {
        return Err(format!(
            "runtime buffer length mismatch: {} != {}",
            raw_buffer.len(),
            BUFFER_TOTAL_BYTES
        ));
    }

    add_epoch_raw(raw_buffer, IDX_RUNTIME_TICK, 1)?;

    let result = {
        let input = input_bytes_view_raw(raw_buffer)?;
        catch_unwind(AssertUnwindSafe(|| host.dispatch_direct(unit_id, input)))
    };
    match result {
        Ok(Ok(output)) => {
            set_header_int_raw(raw_buffer, INT_IDX_STATUS_CODE, 0)?;
            set_diagnostics_text_raw(raw_buffer, "")?;
            write_output_bytes_raw(raw_buffer, &output)?;
            add_epoch_raw(raw_buffer, IDX_OUTPUT_WRITTEN, 1)?;
        }
        Ok(Err(error)) => {
            set_header_int_raw(raw_buffer, INT_IDX_STATUS_CODE, 1)?;
            clear_output_raw(raw_buffer)?;
            set_diagnostics_text_raw(raw_buffer, &error)?;
        }
        Err(payload) => {
            set_header_int_raw(raw_buffer, INT_IDX_STATUS_CODE, 2)?;
            clear_output_raw(raw_buffer)?;
            set_diagnostics_text_raw(raw_buffer, &panic_payload_message(payload))?;
            add_epoch_raw(raw_buffer, IDX_PANIC_STATE, 1)?;
        }
    }
    Ok(())
}

fn header_int_raw(raw_buffer: &[u8], index: u32) -> Result<i32, String> {
    if index >= HEADER_INT_COUNT {
        return Err(format!("invalid header index: {index}"));
    }
    let offset = (OFFSET_HEADER_INTS + index * 4) as usize;
    let bytes: [u8; 4] = raw_buffer[offset..offset + 4]
        .try_into()
        .map_err(|_| "header integer region must be exactly four bytes".to_string())?;
    Ok(i32::from_le_bytes(bytes))
}

fn set_header_int_raw(raw_buffer: &mut [u8], index: u32, value: i32) -> Result<(), String> {
    if index >= HEADER_INT_COUNT {
        return Err(format!("invalid header index: {index}"));
    }
    let offset = (OFFSET_HEADER_INTS + index * 4) as usize;
    raw_buffer[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
    Ok(())
}

fn input_bytes_view_raw(raw_buffer: &[u8]) -> Result<&[u8], String> {
    let length = header_int_raw(raw_buffer, INT_IDX_INPUT_LENGTH)?;
    if length < 0 || length as u32 > INPUT_MAX_BYTES {
        return Err(format!("invalid input length {length}"));
    }
    region_raw(raw_buffer, OFFSET_INPUT_BYTES, length as u32)
}

fn write_output_bytes_raw(raw_buffer: &mut [u8], bytes: &[u8]) -> Result<(), String> {
    if bytes.len() > OUTPUT_MAX_BYTES as usize {
        return Err(format!(
            "output payload too large: {} > {}",
            bytes.len(),
            OUTPUT_MAX_BYTES
        ));
    }
    clear_output_region_raw(raw_buffer)?;
    let output = region_raw_mut(raw_buffer, OFFSET_OUTPUT_BYTES, bytes.len() as u32)?;
    output.copy_from_slice(bytes);
    set_header_int_raw(raw_buffer, INT_IDX_OUTPUT_LENGTH, bytes.len() as i32)
}

fn clear_output_raw(raw_buffer: &mut [u8]) -> Result<(), String> {
    clear_output_region_raw(raw_buffer)?;
    set_header_int_raw(raw_buffer, INT_IDX_OUTPUT_LENGTH, 0)
}

fn clear_output_region_raw(raw_buffer: &mut [u8]) -> Result<(), String> {
    region_raw_mut(raw_buffer, OFFSET_OUTPUT_BYTES, OUTPUT_MAX_BYTES)?.fill(0);
    Ok(())
}

fn set_diagnostics_text_raw(raw_buffer: &mut [u8], message: &str) -> Result<(), String> {
    let bytes = message.as_bytes();
    if bytes.len() > DIAGNOSTIC_MAX_BYTES as usize {
        return Err(format!(
            "diagnostic payload too large: {} > {}",
            bytes.len(),
            DIAGNOSTIC_MAX_BYTES
        ));
    }
    let diagnostics = region_raw_mut(raw_buffer, OFFSET_DIAGNOSTIC_BYTES, DIAGNOSTIC_MAX_BYTES)?;
    diagnostics.fill(0);
    diagnostics[..bytes.len()].copy_from_slice(bytes);
    add_epoch_raw(raw_buffer, IDX_DIAGNOSTICS_WRITTEN, 1)
}

fn add_epoch_raw(raw_buffer: &mut [u8], index: u32, delta: i32) -> Result<(), String> {
    let current = load_epoch_raw(raw_buffer, index);
    store_epoch_raw(raw_buffer, index, current.saturating_add(delta))
}

fn load_epoch_raw(raw_buffer: &[u8], index: u32) -> i32 {
    if index >= EPOCH_SLOT_COUNT {
        return 0;
    }
    let offset = (OFFSET_EPOCHS + index * EPOCH_SLOT_BYTES) as usize;
    let bytes: [u8; 4] = raw_buffer[offset..offset + 4]
        .try_into()
        .unwrap_or_default();
    i32::from_le_bytes(bytes)
}

fn store_epoch_raw(raw_buffer: &mut [u8], index: u32, value: i32) -> Result<(), String> {
    if index >= EPOCH_SLOT_COUNT {
        return Err(format!("invalid epoch index: {index}"));
    }
    let offset = (OFFSET_EPOCHS + index * EPOCH_SLOT_BYTES) as usize;
    raw_buffer[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
    Ok(())
}

fn region_raw(raw_buffer: &[u8], offset: u32, length: u32) -> Result<&[u8], String> {
    let start = offset as usize;
    let end = start + length as usize;
    if end > raw_buffer.len() {
        return Err(format!(
            "runtime region out of bounds: {} + {} > {}",
            offset,
            length,
            raw_buffer.len()
        ));
    }
    Ok(&raw_buffer[start..end])
}

fn region_raw_mut(raw_buffer: &mut [u8], offset: u32, length: u32) -> Result<&mut [u8], String> {
    let start = offset as usize;
    let end = start + length as usize;
    if end > raw_buffer.len() {
        return Err(format!(
            "runtime region out of bounds: {} + {} > {}",
            offset,
            length,
            raw_buffer.len()
        ));
    }
    Ok(&mut raw_buffer[start..end])
}

#[derive(Debug)]
enum FrameReadError {
    EndOfStream,
    Io(io::Error),
}

fn read_frame<R: Read>(reader: &mut R) -> Result<Vec<u8>, FrameReadError> {
    let mut size = [0_u8; 4];
    match reader.read_exact(&mut size) {
        Ok(()) => {}
        Err(error) if error.kind() == io::ErrorKind::UnexpectedEof => {
            return Err(FrameReadError::EndOfStream)
        }
        Err(error) if error.kind() == io::ErrorKind::BrokenPipe => {
            return Err(FrameReadError::EndOfStream)
        }
        Err(error) => return Err(FrameReadError::Io(error)),
    }

    let length = u32::from_le_bytes(size) as usize;
    let mut payload = vec![0_u8; length];
    reader
        .read_exact(&mut payload)
        .map_err(FrameReadError::Io)?;
    Ok(payload)
}

fn write_frame<W: Write>(writer: &mut W, payload: &[u8]) -> io::Result<()> {
    writer.write_all(&(payload.len() as u32).to_le_bytes())?;
    writer.write_all(payload)?;
    Ok(())
}

#[cfg(any(test, target_os = "linux"))]
pub(crate) fn read_frame_for_test<R: Read>(reader: &mut R) -> io::Result<Vec<u8>> {
    match read_frame(reader) {
        Ok(payload) => Ok(payload),
        Err(FrameReadError::EndOfStream) => Err(io::Error::new(
            io::ErrorKind::UnexpectedEof,
            "end of stream",
        )),
        Err(FrameReadError::Io(error)) => Err(error),
    }
}

#[cfg(any(test, target_os = "linux"))]
pub(crate) fn write_frame_for_test<W: Write>(writer: &mut W, payload: &[u8]) -> io::Result<()> {
    write_frame(writer, payload)
}
