# Runtime Native

`runtime-native` is Foundation's native device shell bridge. It makes desktop and
mobile shells first-class without weakening the runtime ladder.

## Contract

1. Tauri is the shell. It owns windows, mobile projects, bundling, and platform entrypoints.
2. `foundation/runtime-native` is the bridge. It owns binary native frames, command allowlists, secure storage surfaces, capability discovery, and dispatch into `runtime-sdk`.
3. `foundation/runtime-sdk` remains the performance anchor. Hot work uses WASM/SAB, Rust FFI, shared memory, framed stdio, WebSocket, or HTTP fallback.
4. Tauri IPC is measured as a control boundary. It is not assumed zero-copy and must not replace direct FFI, shared memory, or WASM/SAB for hot compute.
5. Native dispatch frames are binary. JSON IPC is allowed for low-volume control responses such as capability discovery and secure-store results only.

## Device Access Lanes

Foundation treats device APIs as two separate lanes.

### Lane 1: WebView Compatibility

The frontend may use browser APIs such as `getUserMedia` when a feature only
needs compatibility-level camera or microphone access. This lane returns browser
objects such as `MediaStream`, compressed frames, or WebAudio/WebRTC-shaped
buffers. It is acceptable for preview, simple capture, and compatibility paths.

It is not the Foundation performance lane. It does not provide raw
`CVPixelBuffer`, `android.media.Image`, or direct PCM ownership, and the data
passes through the WebView compositor/runtime before Foundation can process it.
Android WebView also has an additional permission layer beyond the OS dialog, so
camera and microphone behavior must be tested separately from desktop and iOS.

### Lane 2: Native Plugin Byte Lane

Performance-sensitive device access must use a Tauri mobile/native plugin that
bridges Rust to Swift on iOS and Kotlin or Java on Android. This is the
Foundation lane:

```text
iOS AVFoundation / CoreMotion / CoreLocation
Android Camera2 / AudioRecord / SensorManager / platform APIs
        |
        v
Tauri plugin native code
        |
        v
Rust plugin host / foundation-runtime-native
        |
        v
binary native frame or runtime-sdk fixed buffer
        |
        v
runtime-sdk lane planner: FFI, shared memory, WASM/SAB, wgpu, stdio, WS/HTTP fallback
```

The target shape is bytes first. Camera frames should arrive as pixel buffers or
packed planes, microphone frames as PCM, and sensor samples as typed slots. JSON
metadata may describe stream setup, permission state, or diagnostics, but frame
payloads must stay binary.

## Device API Policy

Default scaffold posture is intentionally empty: no device plugin dependencies,
no mobile permissions, and only Foundation native commands in the active
capability list. Device access is added deliberately per app.

| Device surface | Foundation default lane | Notes |
| --- | --- | --- |
| Geolocation | Official Tauri plugin, then Foundation event/metadata | Good config-first candidate. Keep watch streams bounded and cancellable. |
| Biometric | Official Tauri plugin, auth gate only | Do not expose biometric results as durable frontend state. Treat as a native auth assertion. |
| NFC | Official Tauri plugin, binary payload into domain handler | Validate tag payloads before domain use. |
| Barcode scanner | Official Tauri plugin for scan command; native byte lane for custom vision | Built-in scanner is suitable for ordinary scan workflows. Raw camera processing should use a custom camera plugin. |
| Haptics | Official Tauri plugin, UI feedback only | Never couple haptic success to domain success. |
| Notifications | Official Tauri plugin, low-rate control plane | Commands are control events, not stream data. |
| Camera preview | WebView lane acceptable | Use for simple preview or upload workflows. |
| Camera compute | Custom Swift/Kotlin plugin into `runtime-native` | Required for raw frames, `wgpu`, native SIMD, or WASM parity. |
| Microphone preview/call | WebView lane acceptable | Use for normal WebRTC/media compatibility. |
| Microphone compute | Custom Swift/Kotlin plugin into `runtime-native` | Required for raw PCM, DSP, wake-word, biometric audio, or low-latency analysis. |
| IMU/accelerometer/gyroscope | Custom CoreMotion/SensorManager plugin | Model as typed slots with epoch-gated updates. |

Tauri maintains official plugins for device surfaces such as geolocation,
barcode scanning, biometric auth, haptics, NFC, notifications, filesystem, and
shell access. The scaffold must not enable them by default. Add the plugin
dependency, privacy strings, Android permissions/features, and capability
permissions in the same change that introduces the app feature.

## Native Camera And Audio Shape

The first high-performance custom plugins should be camera and microphone:

1. Swift/Kotlin owns the OS capture session.
2. Native code writes frame/audio data into a bounded native buffer, descriptor
   ring, or packet-ring-shaped queue.
3. Rust validates stream id, schema, dimensions/rate, stride, timestamp, and
   frame budget.
4. `runtime-native` dispatches a binary descriptor or buffer reference into
   `runtime-sdk`.
5. The lane planner selects `rust-ffi`, `shared-memory`, `wasm-sab`,
   `wasm-transfer`, `wgpu`, `stdio`, or network fallback according to workload,
   trust, locality, and deadline.

For camera, prefer explicit plane metadata:

```text
stream_id
format: bgra8 | rgba8 | yuv420 | nv12
width
height
stride/plane offsets
timestamp_monotonic_ns
epoch
payload bytes or native buffer handle
```

For microphone, prefer fixed chunking:

```text
stream_id
format: pcm_s16le | pcm_f32le
sample_rate_hz
channels
frames_per_chunk
timestamp_monotonic_ns
epoch
payload bytes or native buffer handle
```

The WebView must not be the owner of raw camera or PCM streams for
performance-critical Foundation work.

## Sensors

Sensor streams fit the existing `runtime-sdk` control-buffer discipline. Use
typed slots, 4-byte-aligned atomic epoch fields, and bounded readers:

```text
slot[n] = { kind, x, y, z, accuracy, timestamp, epoch }
```

WASM/native readers observe epoch changes and process only complete samples.
Blocking waits stay in workers or native host threads, never on the browser main
thread.

## Scaffold

Generated full projects default to:

```text
WITH_WASM=true
WITH_NATIVE=true
native/
foundation/runtime-native/
foundation/runtime-sdk/
```

`native/` contains the Tauri shell. It points to the existing `frontend/` Vite dev server in development and `frontend/dist` for production builds.

## Commands

```bash
make native-dev
make native-build
make native-mobile-init
make native-bench
make native-doctor
```

`native-bench` is report-only until three stable baselines exist for a machine class.

Without Tauri or mobile SDKs, run the local communication simulation directly:

```bash
cargo run --manifest-path foundation/runtime-native/rust/Cargo.toml --release --bin native_flow_sim
```

The simulation is a contract check for communication shape. It compares
full-payload native frames against descriptor-only control frames and the
`runtime-sdk` fixed-buffer path. The expected result is that full-payload
control frames scale linearly with payload size, while descriptor frames stay
constant and move only stream/buffer ownership metadata.

## Security

Native commands are limited to:

- `foundation_runtime_dispatch`
- `foundation_runtime_capabilities`
- `foundation_secure_store_get`
- `foundation_secure_store_put`
- `foundation_secure_store_delete`

Runtime units are allowlisted by descriptor before dispatch. Secure tokens must use native storage surfaces; frontend `localStorage` is not an acceptable token store for native shells.

The initial scaffold includes an in-memory secure-store command implementation as a wiring baseline. Production shells must replace it with the platform keychain, keystore, or credential-manager backend before persisting session material.

Device plugins must follow the same allowlist discipline:

1. No device plugin is active unless its capability is listed in
   `app.security.capabilities`.
2. Every device permission must have a matching product reason in iOS privacy
   strings and Android manifest permissions/features.
3. Remote origins remain unprivileged. Do not expose native device commands to
   remote content.
4. Runtime units that consume device buffers must be allowlisted by descriptor.
5. Oversized, stale, malformed, unauthorized, canceled, and backpressured frames
   must produce controlled errors.

## Boundaries

Foundation can deliver native-class compute for device data by keeping the hot
path in Rust/WASM/shared-memory lanes. It is not a replacement for deep native
iOS or Android product surfaces such as home-screen widgets, App Intents, Siri
shortcuts, or embedded SwiftUI/Compose views. Those require native app layers
outside the Tauri shell and should be planned as app-specific native extensions,
not Foundation scaffold defaults.

## References

- Tauri mobile plugins can call native Kotlin/Java and Swift code:
  <https://v2.tauri.app/develop/plugins/develop-mobile/>
- Tauri plugin catalog and support table:
  <https://v2.tauri.app/plugin/>
- Tauri capabilities and command permissions:
  <https://v2.tauri.app/security/capabilities/>
