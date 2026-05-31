# Foundation Tour

Status: current as of 2026-05-29
Owner: Platform Architecture

This tour follows one product action through Foundation. The example is
generic: a user presses a button to create or update a business record.

## The Short Version

Foundation keeps application work in clear lanes:

1. The app receives a request.
2. The request is wrapped with metadata: who asked, what organization it belongs
   to, correlation ID, request ID, tags, and security context.
3. The domain handler validates the request and writes durable truth through the
   database executor lane.
4. The system emits a lifecycle event: requested, succeeded, failed, or queued.
5. Workers process slow follow-up work without blocking the user request.
6. Redis coordinates cross-process notifications and streams.
7. Hermes keeps a small local hot view of recently needed operational state.
8. Frontend, websocket, workers, and dashboards read the same contract shape.

## Step By Step

### 1. Request Enters

HTTP, websocket, native, or worker input is converted into the same runtime
envelope shape. That gives the system one way to carry request identity,
metadata, tags, correlation, tenant scope, and payload.

### 2. Metadata Is Attached

Metadata is not decoration. It is how Foundation preserves the circumstance of
the request. A record should know the actor, organization, request, source,
tags, and relevant security context that created or changed it.

### 3. Domain Handler Runs

The application service owns business meaning. Foundation owns the shared
execution lanes: validation shape, database helpers, logging, events, workers,
cache, Redis coordination, and hot projections.

### 4. Durable State Is Written

The database lane is the source of truth. Foundation helpers apply timeouts,
pool budgets, transaction discipline, query shape, JSON preservation where
needed, and explicit error classes.

### 5. Events Are Emitted

Every important command has a lifecycle. Events make that lifecycle visible to
workers, realtime updates, incident review, metrics, and future process
visualization.

### 6. Slow Work Leaves The Request Path

Anything slow or retryable moves to workers: notifications, enrichment,
integration calls, document generation, billing summaries, and long-running
state changes. The user request should not wait on work that can safely happen
after the durable command.

### 7. Hermes Serves Hot Operational Reads

Hermes is not the source of truth. It is a bounded local memory of state this
node needs right now. It lets dashboards, websocket fanout, workers, and
repeated reads avoid hammering the durable database for every lookup.

If Hermes is stale, oversized, or rebuilding, it falls back to canonical state
and records that fallback. Correctness wins over speed.

### 8. Observability Closes The Loop

Structured logs, metrics, delivery events, benchmarks, and contract checks make
the system inspectable. A developer or agent can see what was registered, which
routes exist, which handlers are active, whether Hermes is warm, whether Redis
is connected, and whether generated projects drifted from Foundation.

## Why This Matters

Foundation is not trying to make every app identical. It makes the important
system behaviors consistent:

- identity and tenant scope
- request metadata and tags
- database execution discipline
- lifecycle events
- worker boundaries
- realtime coordination
- hot local reads
- security and logging posture
- generated project checks

That lets applications differ in product logic while sharing the same operational
spine.
