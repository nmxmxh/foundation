use std::sync::Mutex;

use ovasabi_runtime_native::NativeRuntimeBridge;
use serde::Serialize;
use tauri::ipc::{InvokeBody, Request, Response};
use tauri::State;

struct NativeState {
    bridge: Mutex<NativeRuntimeBridge>,
}

impl NativeState {
    fn new() -> Self {
        Self {
            bridge: Mutex::new(NativeRuntimeBridge::with_role_limits(Default::default())),
        }
    }
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct Capabilities {
    native: bool,
    native_ffi: bool,
    native_shared_memory: bool,
    wasm_sab: bool,
    wasm_transfer: bool,
    platform: String,
}

#[tauri::command]
fn foundation_runtime_dispatch(
    request: Request<'_>,
    state: State<'_, NativeState>,
) -> Result<Response, String> {
    let InvokeBody::Raw(bytes) = request.body() else {
        return Err("foundation_runtime_dispatch requires a raw binary request body".to_string());
    };
    let bridge =
        state.bridge.lock().map_err(|_| "native runtime bridge lock poisoned".to_string())?;
    let response = bridge.dispatch_frame(bytes).map_err(|error| error.to_string())?;
    Ok(Response::new(response))
}

#[tauri::command]
fn foundation_runtime_capabilities(state: State<'_, NativeState>) -> Result<Capabilities, String> {
    let bridge =
        state.bridge.lock().map_err(|_| "native runtime bridge lock poisoned".to_string())?;
    let capabilities = bridge.capabilities();
    Ok(Capabilities {
        native: capabilities.native,
        native_ffi: capabilities.native_ffi,
        native_shared_memory: capabilities.native_shared_memory,
        wasm_sab: capabilities.wasm_sab,
        wasm_transfer: capabilities.wasm_transfer,
        platform: capabilities.platform,
    })
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let result = tauri::Builder::default()
        .manage(NativeState::new())
        .invoke_handler(tauri::generate_handler![
            foundation_runtime_dispatch,
            foundation_runtime_capabilities
        ])
        .run(tauri::generate_context!());

    if let Err(error) = result {
        eprintln!("tauri runtime failed: {error}");
    }
}
