# config-contracts

`config-contracts` is the shared runtime configuration boundary for frontend and backend apps.

It separates:

1. public runtime config
2. server-only runtime config

Rules:

1. frontend boot must validate public config before mount
2. server env loaders must validate and derive the public subset
3. object-storage, Redis, DB, JWT, and worker tuning remain server-only
4. DB pools, dispatch concurrency, Redis prefix/TTL, and queue budgets must be explicit config, never hardcoded runtime defaults
5. runtime memory, transport, compression, and post-quantum posture must be declared explicitly so scaffolded apps do not drift
6. frontend communication packages are explicit config consumers: `@ovasabi/runtime-transport` chooses HTTP/WebSocket/binary fallback, `@ovasabi/frontend-kit` discovers runtime artifacts, and app code adapts generated protobuf domain types

Planned public/server split:

1. public config
   - API and WS base URLs
   - feature flags
   - auth mode
   - transport timeout budgets
   - communication layer order and envelope schema version
   - wasm asset paths
   - runtime memory mode, transport order, and compression order
   - diagnostics toggles
   - locale defaults
2. server config
   - database and Redis DSNs
   - object-storage endpoint and credentials
   - JWT secrets
   - queue and DB pool tuning
   - Redis key prefix and TTL policy
   - dispatch concurrency budgets and compression strategy
   - post-quantum security posture

Extraction gate:

1. `field_os`, `fintech_v1`, and `reframe_v1` must all derive frontend-safe config from the same schema shape.
