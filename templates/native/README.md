# Native Runtime Scaffold

This folder is the Foundation native shell scaffold. It uses Tauri v2 for the
device shell and `foundation/runtime-native` for binary Foundation dispatch.

The scaffold assumes the standard Foundation layout:

```text
project/
├── frontend/
└── native/
```

`src-tauri/tauri.dev.conf.json` starts the frontend from `../../frontend`.
`src-tauri/tauri.prod.conf.json` builds and bundles `../../frontend/dist`.
If a project changes this layout, update both Tauri config overlays and the
native package scripts together.

Common commands from the project root:

```bash
make native-dev
make native-build
make native-mobile-init
make native-bench
make native-doctor
```

Native IPC is a measured control boundary. Hot compute remains in
`foundation/runtime-sdk` lanes: WASM/SAB, Rust FFI, shared memory, framed stdio,
WebSocket, or HTTP fallback.

## Device Access

The scaffold enables no device plugins by default. That is intentional: every
device surface must be added with a matching Tauri plugin dependency,
capability, iOS privacy string, Android permission/feature, and Foundation
runtime test.

Foundation uses two device lanes:

1. WebView compatibility lane: browser APIs such as `getUserMedia`. Use this for
   preview, simple capture, uploads, and WebRTC-shaped workflows.
2. Native byte lane: Swift/Kotlin platform capture -> Rust plugin host ->
   `foundation/runtime-native` -> `foundation/runtime-sdk`. Use this for raw
   camera frames, microphone PCM, sensors, `wgpu`, WASM parity, and any
   performance-critical stream.

Camera, microphone, and sensor streams should not be routed through JSON or
owned by the WebView compositor when the feature depends on raw buffers or low
latency. Model them as binary frames, native buffer handles, packet-ring
descriptors, or `runtime-sdk` typed slots with epoch gating.

Official Tauri plugins such as geolocation, biometric auth, NFC, haptics,
barcode scanning, notifications, filesystem, and shell access are configuration
work once the product needs them. Keep their capability snippets inactive until
the dependency and product permission copy exist.

The scaffold secure-store commands are wired as native-only commands and avoid
frontend storage. Replace the in-memory baseline with the platform keychain /
keystore implementation before storing durable production session material.

The active capability list is explicit in `src-tauri/tauri.conf.json` and both
environment overlays. Keep example capabilities inactive until the app actually
uses the matching Tauri plugin.

Deep OS integrations such as iOS widgets, App Intents, Siri shortcuts, or
embedded SwiftUI/Compose views are outside the Foundation scaffold baseline.
Plan those as app-specific native extensions.
