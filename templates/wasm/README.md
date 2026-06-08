# WASM Runtime

This module is the project-owned browser runtime compatibility shim. Foundation communication is owned by `runtime-transport`; the Go WASM entrypoint only preserves legacy globals and forwards messages to `window.__OVASABI_RUNTIME_TRANSPORT.dispatch` when present.

## Build

```bash
make build-wasm-dev
make build-wasm
```

Build output is written to `frontend/public/`.

For new low-latency browser compute, prefer the Rust `foundation/runtime-sdk`
path: generated Cap'n Proto runtime contracts, host-managed shared memory,
worker-owned blocking waits, and exported module entrypoints. This Go WASM file
is not the public performance ABI and should not be expanded into app state or
network orchestration.

## Contract

- Runtime-transport owns websocket/http dispatch, binary envelope encoding, compression, and fallback behavior.
- The module exposes `setFrontendReady`, `connectWebSocket`, `disconnectWebSocket`, `sendWasmMessage`, and `emitWasmCompatMessage` on `window` only for compatibility.
- New frontend code should use runtime-transport directly. If legacy code still listens through `window.onWasmMessage`, bridge runtime-transport events into `emitWasmCompatMessage`.
