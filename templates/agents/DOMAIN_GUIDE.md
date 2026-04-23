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

```
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

```
<domain>:<action>:<version>:<state>
```

**Components:**
- `domain`: Business domain (user, incident, media, etc.)
- `action`: The operation (create, update, authenticate, etc.)
- `version`: Schema version (v1, v2)
- `state`: Event state (requested, success, failed, ack)

**Examples:**
```
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
  string name = 2;
  // ... domain-specific fields
  google.protobuf.Timestamp created_at = 10;
  google.protobuf.Timestamp updated_at = 11;
}

// Request messages (what clients send)
message Create<Entity>Request {
  common.v1.RequestMetadata metadata = 1;
  string name = 2;
  // ... creation fields
}

message Get<Entity>Request {
  common.v1.RequestMetadata metadata = 1;
  string id = 2;
}

message List<Entity>Request {
  common.v1.RequestMetadata metadata = 1;
  int32 page_size = 2;
  string page_token = 3;
}

// Response messages (what server returns)
message Create<Entity>Response {
  <Entity> entity = 1;
}

message Get<Entity>Response {
  <Entity> entity = 1;
}

message List<Entity>Response {
  repeated <Entity> entities = 1;
  string next_page_token = 2;
  int32 total_count = 3;
}
```

### 3. Generate bindings

```bash
make proto
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

```
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

## Compression Thresholds

| Payload Size | Format | Compression |
|--------------|--------|-------------|
| < 1KB | JSON | None |
| 1KB - 100KB | Binary | Brotli |
| > 100KB | Binary | Gzip |

The server-kit handles this automatically. Clients can request binary format with `?format=binary`.

---

## Quick Reference: Domain Checklist

- [ ] Create `api/protos/<domain>/v1/<domain>.proto`
- [ ] Include `common.v1.RequestMetadata` in requests
- [ ] Follow event naming: `<domain>:<action>:v1:<state>`
- [ ] Run `make proto` to generate bindings
- [ ] Create `internal/service/<domain>/service.go`
- [ ] Create `internal/service/<domain>/registration.go`
- [ ] Register handlers in `internal/registry/`
- [ ] Write tests in `tests/integration/<domain>_test.go`

---
*Foundation Domain Guide v1.0.0*
