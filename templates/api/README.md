# API Layer

`api/` is the application communication boundary.

## Contents

1. **`api/protos/`** - Protocol buffer contracts for control-plane messaging
2. **`api/schemas/`** - (Optional) Cap'n Proto schemas for hot-path binary payloads

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        Clients                               │
│              (Web, Mobile, External Services)                │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                    API Boundary                              │
│                                                              │
│   ┌─────────────────┐        ┌─────────────────────────┐   │
│   │  api/protos/    │        │  foundation/            │   │
│   │  (App Contracts)│        │  runtime-transport/     │   │
│   │                 │        │  (Envelope & Metadata)  │   │
│   └─────────────────┘        └─────────────────────────┘   │
│                                                              │
└─────────────────────────┬───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                  Internal Services                           │
│           (Business Logic, Data Access, Workers)             │
└─────────────────────────────────────────────────────────────┘
```

## Key Principles

1. **App-owned contracts** live in `api/protos/`
2. **Foundation-owned transport** (envelope, metadata) lives in `foundation/runtime-transport/`
3. **Versioned packages** - all protos use `<domain>/v1/` structure
4. **Idempotent mutations** - requests carry `idempotency_key` for safe retries

## Communication Pattern

Foundation uses **event-driven messaging** over the runtime transport layer:

- **Typed app contracts** - app schemas live in `api/protos` and TypeScript types are generated with `make proto-ts`
- **Shared transport** - browser code uses `@ovasabi/runtime-transport`; backend code uses foundation transport/server-kit primitives
- **Layer fallback** - runtime clients can move across `sab`, `wasm`, `transferable`, `ws`, `http`, and `postMessage` based on capability
- **Event types** follow `<domain>:<action>:<version>:<state>` pattern
- **Compressed binary** - automatic brotli/gzip for large payloads
- **Correlation tracing** - every request carries `correlation_id`

## Adding New Domains

```bash
# Create domain structure
mkdir -p api/protos/<domain>/v1

# Create proto file
touch api/protos/<domain>/v1/<domain>.proto

# Generate bindings
make communication-contracts
```

See `api/protos/README.md` for contract rules and `.agents/DOMAIN_GUIDE.md` for detailed patterns.
