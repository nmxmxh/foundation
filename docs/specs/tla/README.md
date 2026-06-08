# Foundation TLA Spec Templates

Status: starter templates
Owner: Platform Architecture

These modules are small design skeletons for Foundation primitives. Copy one
into an ADR, design branch, or app-specific spec before changing queue, retry,
cache, projection, WebSocket, or runtime fallback behavior.

Use them with the guidance in `../../tla_architecture_practices.md`.

Default template set:

1. `WorkerRetryQueue.tla`: bounded queues, retry budgets, leases, and terminal
   states.
2. `CacheProjectionFreshness.tla`: read-your-write, bounded-stale reads,
   watermarks, and repair.
3. `WebSocketBackpressure.tla`: authenticated subscriptions, bounded write
   queues, slow-client policy, and disconnect cleanup.
4. `FrontendLiveProjection.tla`: generated frontend stores connected to Hermes
   projections, including loading state, buffered live updates, tenant
   rejection, disconnect cleanup, and monotonic versions.

The templates are not production proofs. They are executable design contracts:
name the visible state, name the hidden state, then map every invariant to a
test, benchmark, telemetry field, or runtime check.
