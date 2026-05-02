# frontend-kit

`frontend-kit` is the operational frontend baseline for Ovasabi applications.

It owns:

1. IndexedDB-backed persistence primitives for stores and metadata indexes
2. metadata normalization and envelope context helpers
3. reset handles for coordinated auth/session teardown
4. React hook adapters for runtime, WASM, and SharedArrayBuffer snapshots

It does not own:

1. app domain contracts
2. brand or visual primitives
3. route-specific data fetching
4. product-specific websocket/auth policies

Use `@ovasabi/frontend-kit` with `@ovasabi/ui-minimal`:

```ts
import { createIndexedDBStorage, createRuntimeExternalStore } from "@ovasabi/frontend-kit";
import { MinimalButton } from "@ovasabi/ui-minimal";
```

Contract posture:

1. generated protobuf types live in `frontend/src/types/protos`
2. app stores and hooks should wrap generated contracts
3. `frontend-kit` provides metadata/cache/runtime handles only

Runtime posture:

1. subscribe to epochs or external runtime signals instead of polling component-local timers
2. use cached `DataView`/typed-array readers for SAB regions
3. expose snapshots through `useSyncExternalStore` so React renders only when the snapshot actually changes
4. degrade cleanly when IndexedDB, SharedArrayBuffer, or workers are unavailable
5. discover published WASM artifacts with `loadWasmManifest()` instead of hard-coded module paths
6. instantiate runtime modules through `foundation/runtime-sdk/ts/browser-host`, not through app-local import glue
