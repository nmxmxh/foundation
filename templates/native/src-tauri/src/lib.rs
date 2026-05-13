use std::sync::Mutex;

use ovasabi_runtime_native::{NativeRuntimeBridge, NativeSecureStore};
use serde::Serialize;
use tauri::ipc::{InvokeBody, Request, Response};
use tauri::State;

struct NativeState {
    bridge: Mutex<NativeRuntimeBridge>,
    secure_store: NativeSecureStore,
}

impl NativeState {
    fn new() -> Self {
        Self {
            bridge: Mutex::new(NativeRuntimeBridge::with_role_limits(Default::default())),
            secure_store: NativeSecureStore::default(),
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
fn foundation_runtime_dispatch(request: Request<'_>, state: State<'_, NativeState>) -> Result<Response, String> {
    let InvokeBody::Raw(bytes) = request.body() else {
        return Err("foundation_runtime_dispatch requires a raw binary request body".to_string());
    };
    let bridge = state
        .bridge
        .lock()
        .map_err(|_| "native runtime bridge lock poisoned".to_string())?;
    let response = bridge
        .dispatch_frame(bytes)
        .map_err(|error| error.to_string())?;
    Ok(Response::new(response))
}

#[tauri::command]
fn foundation_runtime_capabilities(state: State<'_, NativeState>) -> Result<Capabilities, String> {
    let bridge = state
        .bridge
        .lock()
        .map_err(|_| "native runtime bridge lock poisoned".to_string())?;
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

#[tauri::command]
fn foundation_secure_store_get(key: String, state: State<'_, NativeState>) -> Result<Option<Vec<u8>>, String> {
    state
        .secure_store
        .get(&key)
        .map_err(|error| error.to_string())
}

#[tauri::command]
fn foundation_secure_store_put(key: String, value: Vec<u8>, state: State<'_, NativeState>) -> Result<(), String> {
    state
        .secure_store
        .put(&key, &value)
        .map_err(|error| error.to_string())
}

#[tauri::command]
fn foundation_secure_store_delete(key: String, state: State<'_, NativeState>) -> Result<bool, String> {
    state
        .secure_store
        .delete(&key)
        .map_err(|error| error.to_string())
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .manage(NativeState::new())
        .invoke_handler(tauri::generate_handler![
            foundation_runtime_dispatch,
            foundation_runtime_capabilities,
            foundation_secure_store_get,
            foundation_secure_store_put,
            foundation_secure_store_delete
        ])
        .run(tauri::generate_context!())
        .expect("error while running Tauri application");
}
