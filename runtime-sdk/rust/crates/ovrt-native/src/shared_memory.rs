use std::env;

use crate::NativeRuntimeHost;

pub fn serve_transport(host: &NativeRuntimeHost) -> Result<(), String> {
    match env::var("OVRT_RUNTIME_TRANSPORT") {
        Ok(mode) if mode.trim().eq_ignore_ascii_case("shm") => serve_shared_memory(host),
        _ => crate::serve_stdio(host),
    }
}

#[cfg(target_os = "linux")]
fn serve_shared_memory(host: &NativeRuntimeHost) -> Result<(), String> {
    use std::fs::OpenOptions;
    use std::io::{self, Write};
    use std::os::unix::fs::FileExt;

    use ovrt_core::BUFFER_TOTAL_BYTES;

    let path = env::var("OVRT_SHM_PATH").map_err(|_| "OVRT_SHM_PATH is required".to_string())?;
    let file = OpenOptions::new()
        .read(true)
        .write(true)
        .open(&path)
        .map_err(|error| format!("open shared memory file: {error}"))?;

    let stdin = io::stdin();
    let stdout = io::stdout();
    let mut reader = stdin.lock();
    let mut writer = stdout.lock();

    loop {
        let unit_id_bytes = match crate::stdio::read_frame_for_test(&mut reader) {
            Ok(payload) => payload,
            Err(error) if error.kind() == io::ErrorKind::UnexpectedEof => return Ok(()),
            Err(error) => return Err(format!("read unit frame: {error}")),
        };
        let unit_id = String::from_utf8(unit_id_bytes).map_err(|error| error.to_string())?;
        let mut raw_buffer = vec![0_u8; BUFFER_TOTAL_BYTES as usize];
        file.read_exact_at(&mut raw_buffer, 0)
            .map_err(|error| format!("read shared buffer: {error}"))?;
        let output = crate::process_runtime_buffer(host, &unit_id, raw_buffer)?;
        file.write_all_at(&output, 0)
            .map_err(|error| format!("write shared buffer: {error}"))?;
        crate::stdio::write_frame_for_test(&mut writer, &[])
            .map_err(|error| format!("write shared memory ack: {error}"))?;
        writer
            .flush()
            .map_err(|error| format!("flush shared memory ack: {error}"))?;
    }
}

#[cfg(not(target_os = "linux"))]
fn serve_shared_memory(_host: &NativeRuntimeHost) -> Result<(), String> {
    Err("shared memory transport is only supported on linux".to_string())
}
