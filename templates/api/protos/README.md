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

# Output location
api/protos/<domain>/v1/<domain>.pb.go
```

## Event Type Mapping

Proto messages map to event types:

| Message | Event Type |
|---------|------------|
| `CreateFooRequest` | `foo:create:v1:requested` |
| `CreateFooResponse` | `foo:create:v1:success` |
| `GetFooRequest` | `foo:get:v1:requested` |
| `ListFooRequest` | `foo:list:v1:requested` |

## Example Domain

See `_template/v1/example.proto` for a complete domain example with:
- Entity definition
- CRUD operations
- Pagination
- Proper metadata usage

---
*Foundation Proto Contracts v1.0.0*
