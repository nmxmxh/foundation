# Tauri Patch Contract

Foundation pins Tauri as a device shell dependency and keeps the fork narrow.

Pinned baseline:

- Tauri v2 line
- Preferred exact crate/CLI pin in scaffold templates: `=2.11.1`

Patch scope:

1. Preserve raw byte request bodies for `foundation_runtime_dispatch`.
2. Return byte responses through `tauri::ipc::Response` so the frontend receives
   an array-buffer-shaped result.
3. Keep Android raw-byte parity with desktop and iOS. If upstream raw IPC differs
   by platform, patch only that boundary.
4. Do not fork window lifecycle, menu, updater, tray, or packaging behavior.
5. Keep mobile/native plugin byte streams compatible with Foundation's binary
   frame and fixed-buffer contracts.

Non-goals:

- Rewriting Tauri's serde command path.
- Routing high-frequency compute through Tauri IPC.
- Loading user-selected native units.
- Building deep OS surfaces such as widgets, App Intents, Siri shortcuts, or
  embedded SwiftUI/Compose screens as Foundation defaults.

Device access rule:

WebView APIs such as `getUserMedia` are compatibility lanes. They are allowed
for preview, simple capture, and WebRTC-shaped features, but they must not own
performance-critical camera, microphone, or sensor streams.

The Foundation lane is native plugin -> Rust -> `runtime-native` ->
`runtime-sdk`. Camera frames, PCM chunks, and sensor samples should arrive as
bytes or typed slots, then flow through FFI, shared memory, WASM/SAB, wgpu,
stdio, or network fallback according to the lane planner. JSON is limited to
setup, permissions, diagnostics, and low-rate control.

Benchmark rule:

Tauri IPC may be promoted only as a native control lane after measured runs show
it beats WS/HTTP for bounded local dispatch. It must not replace direct FFI,
shared memory, or WASM/SAB for hot compute.

Device benchmark rule:

Custom camera, audio, and sensor plugins must benchmark:

1. capture callback to native buffer enqueue,
2. native buffer enqueue to Rust frame validation,
3. Rust frame validation to selected `runtime-sdk` lane,
4. backpressure behavior under stale reader or full ring,
5. cancellation and permission-denied paths.

The acceptable result is not "Tauri IPC is fast"; the acceptable result is that
the selected lane preserves Foundation's copy budget, allocation budget, and
deadline class for the workload.
