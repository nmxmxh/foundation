# server-kit

`server-kit` is the reusable Go backend shell for Ovasabi apps.

It owns the generic backend platform surface:

- event metadata and envelope contracts
- domain errors and HTTP error bodies
- JWT auth and RBAC context helpers
- bounded handler registration and service registry
- route metadata and dispatch request shaping
- graceful success/error signaling and low-cost observability
- resilience, health, metrics, SLO, and profiling primitives
- worker queues, bounded chain execution, and contract-test harnesses
- Redis, database, object-store, eventlog, bulk, and Hermes projection helpers
- startup/runtime composition helpers

It does not own app business services, app schemas, or app-specific route lists.
