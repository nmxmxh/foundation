use std::io::{self, BufReader, BufWriter, Read, Write};
use std::panic::{catch_unwind, AssertUnwindSafe};

use ovrt_core::{
    BUFFER_TOTAL_BYTES, IDX_OUTPUT_WRITTEN, IDX_PANIC_STATE, IDX_RUNTIME_TICK, INT_IDX_STATUS_CODE,
};

use crate::{panic_payload_message, NativeBuffer, NativeRuntimeHost};

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

    let mut buffer = NativeBuffer::new(raw_buffer.to_vec())?;
    let input = buffer.read_input_bytes()?;
    let _ = buffer.add_epoch(IDX_RUNTIME_TICK, 1)?;

    let result = catch_unwind(AssertUnwindSafe(|| host.dispatch_direct(unit_id, &input)));
    match result {
        Ok(Ok(output)) => {
            buffer.set_header_int(INT_IDX_STATUS_CODE, 0)?;
            buffer.set_diagnostics_text("")?;
            buffer.write_output_bytes(&output)?;
            let _ = buffer.add_epoch(IDX_OUTPUT_WRITTEN, 1)?;
        }
        Ok(Err(error)) => {
            buffer.set_header_int(INT_IDX_STATUS_CODE, 1)?;
            buffer.clear_output()?;
            buffer.set_diagnostics_text(&error)?;
        }
        Err(payload) => {
            buffer.set_header_int(INT_IDX_STATUS_CODE, 2)?;
            buffer.clear_output()?;
            buffer.set_diagnostics_text(&panic_payload_message(payload))?;
            let _ = buffer.add_epoch(IDX_PANIC_STATE, 1)?;
        }
    }
    raw_buffer.copy_from_slice(buffer.into_inner().as_slice());
    Ok(())
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
    reader.read_exact(&mut payload).map_err(FrameReadError::Io)?;
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
        Err(FrameReadError::EndOfStream) => {
            Err(io::Error::new(io::ErrorKind::UnexpectedEof, "end of stream"))
        }
        Err(FrameReadError::Io(error)) => Err(error),
    }
}

#[cfg(any(test, target_os = "linux"))]
pub(crate) fn write_frame_for_test<W: Write>(writer: &mut W, payload: &[u8]) -> io::Result<()> {
    write_frame(writer, payload)
}
