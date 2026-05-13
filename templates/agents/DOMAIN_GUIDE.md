# Domain Definition Guide

> How to transition from foundation boilerplate to app-specific domains.

## Understanding the Architecture

Foundation provides:

- **Transport Layer**: WebSocket + HTTP with envelope-based messaging
- **Event Bus**: Redis-backed pub/sub for async communication
- **Compression**: Brotli/gzip/flate for binary payloads
- **Security**: JWT auth, rate limiting, capability-based access

You provide:

- **Domain Protos**: Your business contracts in `api/protos/<domain>/v1/`
- **Service Handlers**: Event handlers in `internal/service/<domain>/`
- **Domain Logic**: Business rules, validation, state management

## Proto File Structure

```text
api/
├── README.md                    # API boundary explanation
└── protos/
    ├── README.md                # Contract rules
    ├── common/v1/
    │   └── metadata.proto       # Shared metadata fields
    ├── <domain>/v1/
    │   └── <domain>.proto       # Domain-specific messages
    └── _template/v1/
        └── example.proto        # Reference template
```

## Event Naming Convention

Foundation uses a consistent event naming pattern:

```text
<domain>:<action>:<version>:<state>
```

**Components:**

- `domain`: Business domain (user, incident, media, etc.)
- `action`: The operation (create, update, authenticate, etc.)
- `version`: Schema version (v1, v2)
- `state`: Event state (requested, success, failed, ack)

**Examples:**

```text
user:authenticate:v1:requested    # Client requests auth
user:authenticate:v1:success      # Auth succeeded
user:authenticate:v1:failed       # Auth failed

incident:create:v1:requested      # Create incident request
incident:create:v1:success        # Incident created
incident:update:v1:requested      # Update incident
incident:list:v1:requested        # List incidents
```

## Creating a Domain Proto

### 1. Create the folder structure

```bash
mkdir -p api/protos/<domain>/v1
```

### 2. Define your proto file

```protobuf
// api/protos/<domain>/v1/<domain>.proto
syntax = "proto3";

package <domain>.v1;

option go_package = "github.com/ovasabi/<project>/api/protos/<domain>/v1;<domain>v1";

import "google/protobuf/timestamp.proto";
import "common/v1/metadata.proto";

// Core entity
message <Entity> {
  string id = 1;
  string organization_id = 2;
  google.protobuf.Timestamp created_at = 3;
  google.protobuf.Timestamp updated_at = 4;
  string name = 11;
  // ... domain-specific fields start at 11
}

// Request messages (what clients send)
message Create<Entity>Request {
  common.v1.RequestMetadata metadata = 1;
  string name = 11;
  // ... creation fields
}

message Get<Entity>Request {
  common.v1.RequestMetadata metadata = 1;
  string id = 2;
}

message List<Entity>Request {
  common.v1.RequestMetadata metadata = 1;
  int32 page_size = 11;
  string page_token = 12;
}

message Update<Entity>Request {
  common.v1.RequestMetadata metadata = 1;
  string id = 2;
  string name = 11;
  // ... mutable fields
}

message Delete<Entity>Request {
  common.v1.RequestMetadata metadata = 1;
  string id = 2;
}

// Response messages (what server returns)
message Create<Entity>Response {
  common.v1.ResponseMetadata metadata = 1;
  <Entity> entity = 2;
}

message Get<Entity>Response {
  common.v1.ResponseMetadata metadata = 1;
  <Entity> entity = 2;
}

message List<Entity>Response {
  common.v1.ResponseMetadata metadata = 1;
  repeated <Entity> entities = 2;
  string next_page_token = 3;
  int32 total_count = 4;
}

message Update<Entity>Response {
  common.v1.ResponseMetadata metadata = 1;
  <Entity> entity = 2;
}

message Delete<Entity>Response {
  common.v1.ResponseMetadata metadata = 1;
  string id = 2;
  bool deleted = 3;
}
```

### 3. Generate bindings

```bash
make communication-contracts
```

## Common Metadata Pattern

All requests should include metadata for tracing and context:

```protobuf
// api/protos/common/v1/metadata.proto
syntax = "proto3";

package common.v1;

option go_package = "github.com/ovasabi/<project>/api/protos/common/v1;commonv1";

import "google/protobuf/timestamp.proto";

message RequestMetadata {
  string correlation_id = 1;      // Request tracing
  string idempotency_key = 2;     // Duplicate prevention
  string device_id = 3;           // Client device identifier
  string user_id = 4;             // Authenticated user (server-set)
  string organization_id = 5;     // Tenant context (server-set)
  google.protobuf.Timestamp timestamp = 6;
}

message ResponseMetadata {
  string correlation_id = 1;
  string request_id = 2;
  int64 processing_time_ms = 3;
}
```

## Registering Event Handlers

```go
// internal/service/<domain>/registration.go
package <domain>

import (
    "github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
)

func Register(reg *registry.Registry, svc *Service) {
    reg.Handle("<domain>:create:v1:requested", svc.Create)
    reg.Handle("<domain>:get:v1:requested", svc.Get)
    reg.Handle("<domain>:list:v1:requested", svc.List)
    reg.Handle("<domain>:update:v1:requested", svc.Update)
    reg.Handle("<domain>:delete:v1:requested", svc.Delete)
}
```

## Communication Flow

Generated domains should follow the Foundation nervous-system lifecycle:

```text
client command
-> RuntimeEnvelope
-> auth, tenant, correlation, idempotency validation
-> <domain>:<action>:v1:requested
-> optional worker/cache/Redis coordination
-> <domain>:<action>:v1:success or :failed
-> realtime projection/store update
-> frontend state
```

```text
┌─────────┐    WebSocket    ┌─────────┐    Event Bus    ┌─────────┐
│ Client  │ ──────────────► │ Server  │ ──────────────► │ Workers │
│         │                 │         │                 │         │
│ Envelope│                 │ Handler │                 │ Async   │
│ (JSON/  │ ◄────────────── │ Dispatch│ ◄────────────── │ Jobs    │
│ Binary) │    Response     │         │    Results      │         │
└─────────┘                 └─────────┘                 └─────────┘
```

1. Client sends envelope with `event_type` and `payload`
2. Server validates, enriches metadata, dispatches to handler
3. Handler processes, may emit async events to workers
4. Response envelope sent back with same `correlation_id`

`make communication-contracts` generates `tests/contract/generated_lifecycle_test.go` for mutating proto request/response pairs. Those generated tests call `server-kit/go/contracttest.VerifyCommandLifecycle`; implementation and integration tests should reuse that verifier with real observed envelopes/jobs.

For handler tests, wrap the event bus with `contracttest.NewLifecycleRecorder().WrapBus(bus)`, record any enqueued `worker.Job`, then call the generated `verifyGeneratedLifecycleObservation` helper for the relevant contract case.

## Compression Thresholds

| Payload Size | Format | Compression |
| -------------- | -------- | ------------- |
| < 1KB | JSON | None |
| 1KB - 100KB | Binary | Brotli |
| > 100KB | Binary | Gzip |

The server-kit handles this automatically. Clients can request binary format with `?format=binary`.

---

## Quick Reference: Domain Checklist

- [ ] Create `api/protos/<domain>/v1/<domain>.proto`
- [ ] Include `common.v1.RequestMetadata` in requests
- [ ] Follow event naming: `<domain>:<action>:v1:<state>`
- [ ] Run `make communication-contracts` to generate bindings and lifecycle tests
- [ ] Create `internal/service/<domain>/service.go`
- [ ] Create `internal/service/<domain>/registration.go`
- [ ] Register handlers in `internal/registry/`
- [ ] Write tests in `tests/integration/<domain>_test.go`

---
Foundation Domain Guide v{{FOUNDATION_VERSION}}
