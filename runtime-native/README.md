# Foundation Runtime Native

`runtime-native` is the Foundation native shell and bridge layer. It hosts native
device surfaces such as Tauri while keeping high-throughput work on the existing
`runtime-sdk` lanes: WASM/SAB, Rust FFI, shared memory, framed stdio, WebSocket,
and HTTP fallback.

The module is intentionally split:

- `rust/` owns binary native frame validation, unit allowlists, secure storage
  interfaces, and dispatch into `runtime-sdk`.
- `ts/` exposes `@ovasabi/runtime-native`, a `runtime-transport` strategy for
  native shells.
- `TAURI_PATCHES.md` records the narrow upstream Tauri patch contract. Tauri is
  the device shell, not the hot-path runtime.

JSON IPC remains a compatibility/control shape only. Foundation hot paths must
use binary envelopes, fixed runtime buffers, or explicit runtime-sdk lanes.
