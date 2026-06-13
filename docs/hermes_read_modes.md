# Hermes Read Mode Contract

Status: **stable v1.0** — read modes are finalized and backward-compatible.
App teams may build fenced read paths against this contract.
Date: 2026-06-08
Owner: Platform Architecture

Related: [`hermes_hotplane.md`](hermes_hotplane.md),
[`projection_freshness_contract.md`](projection_freshness_contract.md)

---

## Overview

Hermes exposes four read modes for every projection call. These modes control
how staleness, watermark fences, and Postgres fallback interact. They are now
**stable** and backward-compatible. A fifth mode (`optimistic`) is reserved for
future use and must not be used in product code until promoted.

Changing the behavior of any stable mode is a **breaking contract change**
requiring a version bump in `hermes_hotplane.md` and an ADR.

---

## Mode Reference

### `fenced`

> "Give me a read that has definitely seen at least this watermark."

**Behavior:**

- The caller supplies a minimum source watermark, event ID, revision, or
  `updated_at` fence value.
- Hermes checks the partition's current `source_watermark` against the fence.
- If the partition is behind the fence, Hermes waits up to a short bounded
  timeout (default: 50ms, configurable via `HermesReadFenceTimeoutMs`).
- If the fence is not satisfied within the timeout, Hermes falls back to
  Postgres and emits `hermes.read.fallback` metric with reason `fence_timeout`.
- If Hermes is `degraded` or `fallback`, the call goes directly to Postgres.

**Use when:** The caller just wrote a record and wants to read it back (read-your-write), or a downstream consumer needs to confirm a specific event has been projected.

**Not for:** Unauthenticated reads, bulk analytics, or any path where the
fence value comes from client-supplied input without server-side validation.

**Tenant rule:** The fence is always scoped to the calling tenant's partition.
Cross-tenant fence comparisons are rejected with `ErrInvalidScope`.

**Invariant:** `HermesFenceSatisfied` — fenced reads only answer when the
partition has reached or exceeded the required source watermark.

---

### `live`

> "Give me the current projection with metadata so the caller can reason about freshness."

**Behavior:**

- Returns the current node-local snapshot for the tenant/domain/collection scope.
- Response includes `epoch`, `source_watermark`, and `fresh_at` fields.
- No wait, no fence check.
- If the partition is `cold` or `rebuilding`, returns `ErrProjectionBusy` with
  a retry hint — callers must handle this gracefully and not treat it as
  data corruption.
- If the partition is `degraded` or `fallback`, reads are served from Postgres.

**Use when:** Dashboard panels, WebSocket fanout, and operational views where
the caller understands it may see data up to `freshness_budget` seconds old and
includes the `source_watermark` in the response for the consumer to evaluate.

**Not for:** Authorization decisions, balance reads, uniqueness checks, or any
read where staleness could produce an incorrect business outcome.

**Invariant:** `HermesEpochMonotonic` — the epoch returned is always ≥ any
prior epoch returned for the same partition.

---

### `stale_while_revalidate`

> "Serve the current local snapshot immediately; schedule a refresh in the background if it's getting old."

**Behavior:**

- Returns the current local snapshot immediately without blocking.
- If `fresh_at` is more than `staleness_budget_ms` milliseconds ago, Hermes
  schedules a background refresh of the partition from the source stream.
- Callers receive the stale data without error; the response includes
  `stale: true` and `fresh_at` so the caller can display a freshness indicator.
- Background refresh is bounded — it will not re-fetch if a refresh is already
  in flight for the same partition.

**Use when:** UI panels that can show "as of X seconds ago" indicators, non-
critical aggregate reads, and cases where a slightly stale read is always
preferable to a visible loading state.

**Not for:** Any write-following read, any read where the caller acts on the
data immediately without human review, or any security/billing read.

**Invariant:** `HermesReplayable` — the background refresh always fetches
from canonical storage, so the worst case is a momentary stale window before
convergence.

---

### `postgres_required`

> "Bypass Hermes entirely; read directly from Postgres."

**Behavior:**

- Hermes is not consulted. The call goes directly to the Postgres executor with
  the standard `database.AtomicLane` discipline.
- No epoch, watermark, or freshness metadata is attached.
- The read is always consistent with the latest committed Postgres state.
- This mode is the correct choice whenever correctness absolutely requires it.

**Use when:**

- Command acceptance (can the action be performed given current state?)
- Authorization decisions (does this user own this resource?)
- Balance and quota reads (how much has been consumed?)
- Uniqueness checks (does this identifier already exist?)
- Any read inside a transaction that also writes

**Not for:** Repeated hot reads in a high-frequency fanout path — use `live` or
`stale_while_revalidate` for those after benchmarking that Postgres pressure is
the bottleneck.

**Note:** `postgres_required` is the default for reads that do not explicitly
opt into a Hermes mode. Any new read path that does not specify a mode is
implicitly `postgres_required`.

---

## Mode Selection Flowchart

```text
Is the read security-critical, authorization, or balance?
  → postgres_required

Did the caller just write something and need to read it back?
  → fenced (supply the write's returned watermark/revision as fence)

Does the caller need current data with freshness metadata for display?
  → live

Is a slightly stale read always acceptable and speed is important?
  → stale_while_revalidate

Otherwise:
  → postgres_required (safe default)
```

---

## Freshness Budget Configuration

Each projection spec declares its `freshness_budget_ms`. Reads using `live` or
`stale_while_revalidate` respect this budget:

| Projection class | Recommended budget |
| --- | --- |
| Dashboard summary panels | 5000 ms |
| WebSocket fanout state | 500 ms |
| Operational admin reads | 2000 ms |
| Real-time activity feeds | 250 ms |

These are starting points. Adjust based on measured Hermes lag metrics
(`hermes.partition.lag`).

---

## Error Classes

| Error | Meaning | Caller action |
| --- | --- | --- |
| `ErrProjectionBusy` | Partition is cold or rebuilding | Retry with backoff or fall back to `postgres_required` |
| `ErrFenceTimeout` | Fence not satisfied within timeout | Fall back to `postgres_required` with fenced Postgres read |
| `ErrInvalidScope` | Cross-tenant fence or scope violation | Return 400/422 to caller, do not retry |
| `ErrPartitionBounds` | Scope exceeds configured Hermes bounds | Fall back to `postgres_required` for this scope |
| `ErrProjectionUnavailable` | Partition is in `fallback` state | Hermes is already using Postgres; result is correct |

---

## Backward Compatibility

This contract is stable. The following are **non-breaking** changes:

- Adding new optional response metadata fields
- Reducing (not increasing) default timeout values with a config override path
- Adding new error classes for new error conditions

The following require a version bump and ADR:

- Changing the semantics of an existing mode
- Removing a mode
- Changing error class names or their recovery semantics
- Changing default timeout values in ways that cannot be overridden

---

## Agent Checklist

Before using a Hermes read mode in app code, verify:

- [ ] Mode is appropriate for the data sensitivity (see flowchart above)
- [ ] `postgres_required` is used for any security, authorization, or balance read
- [ ] `fenced` fence values come from server-side context, not client input
- [ ] `live` and `stale_while_revalidate` callers handle `ErrProjectionBusy`
- [ ] Freshness budget is declared in the projection spec
- [ ] Metric names match `hermes.read.*` taxonomy
- [ ] Read is tested against tenant-scoped data, not cross-tenant fixtures
