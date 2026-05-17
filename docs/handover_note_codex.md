# Handover Note: Codex

**Project**: Ovasabi Foundation
**Status**: Production scaffold and runtime substrate

## Context

Ovasabi Foundation is a shared scaffold for tenant-isolated, event-driven,
realtime applications. It combines Go backend orchestration, TypeScript/React
frontends, Rust/WASM/native runtime lanes, PostgreSQL durability, Redis
ephemeral coordination, and River-backed worker execution.

## Core Achievements

1. Same-process direct frame dispatch with zero-allocation benchmark targets.
2. A canonical command lifecycle:
   `RuntimeEnvelope -> auth/tenant/correlation/idempotency validation -> requested event -> success/failed event -> frontend projection`.
3. A performance ladder that separates direct calls, binary frames, protobuf,
   gRPC, JSON compatibility, FFI, shared memory, stdio, WASM/SAB, WebSocket,
   HTTP, and native shell control lanes.
4. Generated lifecycle contract tests from mutating protobuf request/response
   pairs.
5. Strict scaffold checks for package boundaries, bounded work, Redis/Postgres
   semantics, Go concurrency ownership, and server-kit runtime usage.

## Current Priorities

1. Keep the scaffold slim: platform modules provide runtime primitives;
   generated apps own product behavior.
2. Raise testing standards toward 95% coverage for new and changed production
   code, with Docker-backed integration tests for app-owned Postgres/Redis/River
   flows.
3. Keep service-backed Foundation Core benchmarks outside generated projects;
   scaffolded apps inherit test targets and checks, not core benchmark daemons.
4. Align template versions with current supported toolchains and keep local
   module directives pinned only to installed toolchains that can be verified.
5. Treat AI/runtime extensions as ordinary product capabilities inside the
   existing Foundation lifecycle, not as a separate platform layer.

## Operating Rule

Prefer small, measured improvements over broad new abstractions. Every runtime
claim should have a contract, a test, a benchmark where relevant, and a clear
fallback path.
