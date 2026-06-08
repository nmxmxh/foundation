# INOS Runtime Reuse Plan

Status: initial implementation guide
Owner: Platform Runtime

## Purpose

Foundation should reuse the INOS runtime shape without importing INOS as an app
dependency or adopting a bindgen-first public API.

The reusable pattern is:

1. TypeScript owns the host environment, workers, shared memory allocation,
   capability discovery, and React/store adaptation.
2. Rust owns hot runtime primitives: bounded shared-memory access, epochs,
   ring buffers, module registries, Cap'n Proto frame validation, hashing,
   compression, content-addressed storage, and compute/storage units.
3. `runtime-transport` owns backend communication, WebSocket, bulk/media
   streaming, fallback, and diagnostics.
4. `frontend-kit` owns app-facing state, generated-store helpers, dummy data,
   tenant projection stores, reset behavior, and runtime snapshot adapters.

## What Foundation Lifts From INOS

Foundation should lift the architecture and small reusable primitives, not the
whole product-specific runtime.

Use these INOS sources as reference material:

- `inos_v1/modules/sdk`: `SafeSAB`, `Epoch`, `Reactor`, `RingBuffer`,
  registry, syscalls, hashing/compression, identity.
- `inos_v1/modules/compute`: `compute_execute`, `compute_dispatch`,
  `ComputeEngine`, `UnitProxy`, capability registry.
- `inos_v1/modules/storage`: bare-metal storage, CAS, encryption pipeline.
- `inos_v1/frontend/src/wasm`: module loader, dispatcher, bridge state,
  registry reader, workers, runtime capability checks.
- `inos_v1/protocols/schemas`: Cap'n Proto contracts for SAB layout,
  syscalls, compute capsules, and local runtime descriptors.

## What Foundation Does Not Lift

- INOS product naming, economics, model registry assumptions, or application
  roles.
- Raw app-local global names as the only contract. Foundation may support
  compatibility globals, but the public contract is typed host-managed memory.
- A wasm-bindgen generated API surface as the public runtime boundary.
- P2P, mesh, active signaling, peer delegation, and browser WebRTC scope.

## Contract Posture

Foundation uses two contract families:

- Protobuf remains the app/domain/backend contract for business services,
  Hermes projection events, durable API messages, and generated TypeScript
  domain types.
- Cap'n Proto is the runtime contract for SAB layouts, syscalls, runtime
  module descriptors, compute capsules, chunk descriptors, and zero-copy
  descriptors.

The runtime public ABI is stable exported entrypoints:

```text
*_init_with_sab
*_alloc
*_free
compute_execute
compute_dispatch
optional *_poll / *_diagnostics
```

Compatibility shims may exist for module loading, but Foundation callers should
not depend on generated JS glue as the module execution contract.

## Initial Foundation Modules

`runtime-sdk/ts/browser-host` gains:

- `RuntimeBridge`: cached SAB/ArrayBuffer views, epoch subscriptions, region
  readers, and safe atomic fallbacks.
- `RuntimeModuleLoader`: host-managed WASM loader with stable init entrypoint
  discovery.
- `RuntimeDispatcher`: bounded generic `library:method` dispatch over
  exported runtime entrypoints or worker-backed executors.
- `RuntimeWorkerPool`: bounded request/response worker protocol.
- `RuntimeRegistryReader`: generated-layout friendly module/capability reader.

`frontend-kit` gains:

- generated-store helpers,
- dummy data factories,
- tenant projection stores,
- generated hook registries,
- runtime workbench adapters over `runtime-sdk` and `runtime-transport`.

## Security Rules

1. Backend derives tenant and authority. Frontend tenant IDs are hints only.
2. Runtime frames must validate size, schema version, route parts, epochs, and
   capability scope before dispatch.
3. No secrets or bearer tokens in SAB regions, runtime logs, dummy fixtures,
   or frontend persistence.
4. Runtime compute accelerates prototype transport and processing; it does not
   own private durable tenant state.

## Evidence Required

Every runtime reuse change should leave at least one of:

- Rust test for bounds, ring-buffer, registry, syscall, or compute behavior.
- TypeScript test for bridge, worker, dispatcher, registry, or module loader.
- TLA/spec note for runtime compute, chunk store, or projection state.
- Benchmark for ABI, SAB, Cap'n Proto, worker, CAS, or projection path.
