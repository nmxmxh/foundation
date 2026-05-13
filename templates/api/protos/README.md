# Proto Contracts

Application-specific protocol buffer definitions.

## Package Structure

```
protos/
├── common/v1/           # Shared types (metadata, pagination, errors)
│   └── metadata.proto
├── <domain>/v1/         # Domain-specific contracts
│   └── <domain>.proto
└── _template/v1/        # Reference templates
    └── example.proto
```

## Contract Rules

### 1. Versioning
- All packages use explicit versioning: `<domain>.v1`
- Breaking changes require new version: `<domain>.v2`
- Old versions maintained for backward compatibility

### 2. Request Metadata
Every mutating request MUST include shared metadata:

```protobuf
message CreateFooRequest {
  common.v1.RequestMetadata metadata = 1;  // Required
  // ... domain fields
}
```

### 3. Idempotency
- Mutations carry `idempotency_key` in metadata
- Handlers must check for duplicate processing
- Safe retries enabled by default

### 4. Field Numbering
- Reserve 1-10 for common fields (id, metadata, timestamps)
- Domain-specific fields start at 11
- Never reuse deleted field numbers

## Frozen Packages

Once a domain is in production, its package is "frozen":
- No breaking changes to existing messages
- New fields must be optional
- Use `reserved` for deprecated fields

## Generating Bindings

```bash
# Generate Go code
make proto

# Generate frontend TypeScript code
make proto-ts

# Regenerate app and foundation communication bindings together
make communication-contracts

# Generate proto-derived VerifyCommandLifecycle tests
make lifecycle-contracts

# Output locations
api/protos/<domain>/v1/<domain>.pb.go
frontend/src/types/protos/<domain>/v1/<domain>.ts
tests/contract/generated_lifecycle_test.go
```

## Event Type Mapping

Proto messages map to event types:

| Message | Event Type |
|---------|------------|
| `CreateFooRequest` | `foo:create:v1:requested` |
| `CreateFooResponse` | `foo:create:v1:success` |
| `UpdateFooRequest` | `foo:update:v1:requested` |
| `UpdateFooResponse` | `foo:update:v1:success` |
| `DeleteFooRequest` | `foo:delete:v1:requested` |
| `DeleteFooResponse` | `foo:delete:v1:success` |
| `GetFooRequest` | `foo:get:v1:requested` |
| `ListFooRequest` | `foo:list:v1:requested` |

Mutating request/response pairs also generate lifecycle contract vectors for `:success` and `:failed`.
Those generated tests call `server-kit/go/contracttest.VerifyCommandLifecycle` so correlation,
tenant, idempotency, worker metadata, and terminal-event refinement are enforced by the scaffold.

## Example Domain

See `_template/v1/example.proto` for a complete domain example with:
- Entity definition
- CRUD operations
- Pagination
- Proper metadata usage

---
*Foundation Proto Contracts v{{FOUNDATION_VERSION}}*
