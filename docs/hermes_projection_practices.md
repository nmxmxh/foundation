# Hermes Projection Practices

Status: active practice
Date: 2026-10-02
Owner: Platform Architecture

## Purpose

This document explains how to configure and operate the Hermes projection layer.
It also explains how to avoid the performance mistakes that four deployed
projects made.

Read this document before you change any Hermes configuration.
Read it again before you add an indexed field.

## What Hermes does

Hermes keeps a bounded, node-local copy of records that this node reads often.

The rule is short:

```text
Postgres decides.
Redis coordinates.
Hermes remembers recent reads for this node.
```

Hermes is not a source of truth.
Any record in Hermes can be discarded and rebuilt from Postgres.

## Read paths

Hermes serves reads through four paths.
Choose the cheapest path that answers the question.

| Path | Use it for | Cost |
| --- | --- | --- |
| `GetRecord` | One record by identifier | One map lookup |
| `Count` | Number of matches | Index walk, no copies |
| `ForEachView` | Stream borrowed records | No record copies |
| `GetColumnarBatch` | Many records for analysis | Materializes column vectors |

NOTE: `ListRecords` deep-copies every record.
Prefer `ForEachView` when the caller does not retain the records.

## Write paths

| Path | Use it for |
| --- | --- |
| `ProjectedRuntimeStore.UpsertRecord` | Normal command-driven writes |
| `MirrorSweeper` | Converging a source table after direct writes |
| `ApplyBatch` / `ApplyRecords` | Batched or replayed events |
| `BulkLoad` | Replacing a whole scope |

`MirrorSweeper` polls a source table for rows changed after a cursor.
It holds a per-source lock across the database read.
One slow source does not block another source.

## Configuration

Hermes accepts three bounds per scope.

| Bound | Field | Default | Purpose |
| --- | --- | --- | --- |
| Record count | `MaxRecords` | 100,000 | Caps live records |
| Byte size | `MaxBytes` | 64 MiB | Caps retained memory |
| Applied events | `MaxAppliedEvents` | 100,000 | Caps the replay fence |

`RuntimeStoreOptions` names these fields `MaxRecordsPerScope` and
`MaxBytesPerScope`.
`ProjectedRuntimeStore` applies them to every scope.

Set the bounds from measured scope size.
Do not copy a default without checking the scope.

## Indexed fields

This is the section that matters most for performance.

### The cardinality rule

Every distinct indexed value retains one bit per slot in the scope.

Memory therefore grows as:

```text
retained bytes = distinct values x slots / 8
```

A field with one value per record retains one full-width bitmap per record.

| Cardinality | Example | Bitmap verdict |
| --- | --- | --- |
| Low | `status`, `state`, `kind`, `audience` | Good |
| Medium | `symbol`, `feed`, `category` | Acceptable |
| High | `slug`, `prefix8`, composite key | Poor |
| Per record | `uuid`, `email`, `timestamp` | Worst |

### Never index an identifier

Do not index `id`, `uuid`, `email`, or a timestamp.
Those fields hold one distinct value per record.
Hermes retains a full-width bitmap for each value.

Index the *state* of a record instead of its identity.

### Audit the fields you configure

A configured field that no code writes costs almost nothing at runtime.
Hermes performs one failed lookup per record per field on the write path.

Measured cost of three unused indexed fields, on a 20,000-record scope:

| Indexed fields | ns/op | allocs/op |
| --- | --- | --- |
| None | 10,470 | 22 |
| One live field | 15,296 | 31 |
| One live field plus three unused | 14,461 | 31 |
| One live field plus eight unused | 14,570 | 31 |

Unused fields cost no measurable time and no extra allocations.

WARNING: an unused field is a latent fault, not a free field.
It costs nothing until the first record carries it.
A field such as `bucket` is harmless while absent and pathological once written.
A field such as `prefix8` already saturates the value cap in one deployed project.

Remove unused fields from configuration.
They mislead operators who read the configuration.

### Fields that were never written anywhere

Four deployed projects shipped the scaffold default list.
Three of its five fields were never written by any production code.

| Field | pronto_v1 | chowdash_rider_v1 | ovasabi_v1 | trotters_v1 |
| --- | --- | --- | --- | --- |
| `state` | live | live | 1 of 5 partitions | 2 of 15 scopes |
| `status` | live, free text | live | 3 of 5 partitions | 4 of 15 scopes |
| `type` | unused | unused | unused | unused |
| `kind` | unused | unused | unused | unused |
| `bucket` | unused | unused | unused | unused |

In chowdash_rider_v1, `bucket` is not a column in any of 32 migrations.
It appears only in a scaffold test fixture.

Audit procedure:

1. Read the configured index list.
2. Search the writers for each field name.
3. Remove each field that no writer sets.
4. Prefer a field with a closed enum over free text.

## The bitmap value cap

Hermes caps distinct indexed values per scope.
The cap derives from the byte budget and the live slot count.

```text
cap = (MaxBytes / 4) / ceil(slots / 8)
```

The cap keeps retained bitmap memory near one quarter of the byte budget.
A flat constant cannot do this, because retained bytes scale with slot count.

| Scope shape | Cap | Retained |
| --- | --- | --- |
| 16 MiB budget, 10,000 slots | 3,355 | 4.0 MB |
| 16 MiB budget, 50,000 slots | 671 | 4.0 MB |
| 32 MiB budget, 50,000 slots | 1,342 | 8.0 MB |
| 16 MiB budget, 2,000 slots | 16,777 | 4.0 MB |

NOTE: a low-cardinality field stays below the cap at every scope size.
The cap never clips an enum field in practice.

### What happens past the cap

Hermes stops indexing new values for that scope.
The scope reports itself saturated.
A query that names an unindexed value falls back to the ordinary field index.

The fallback is a field index walk.
The fallback is not a full-scope scan.

Measured on a 10,000-record scope with two filters, one of them an identifier:

| Path | ns/op | B/op |
| --- | --- | --- |
| Bitmap (uncapped) | 549 | 1,136 |
| Field index (capped) | 885 | 192 |

The capped path costs about 0.34 microseconds more.
The capped path allocates less memory per query.

CAUTION: do not lower the byte budget to force the cap.
Lower the budget to control record memory.

### Why the cap exists

Without the cap, a 10,000-record scope with six identifier fields retains:

```text
13,339 distinct values x 10,000 slots / 8 = 16.7 MB
```

That exceeds the whole 16 MiB byte budget.
The record data in that scope occupies far less.

## Versions and watermarks

### Version semantics

Hermes assigns a version to each accepted write.
The version controls last-writer-wins, tombstone ordering, and replay rejection.

`ProjectedRuntimeStore` assigns a process-local counter that resets at boot.

That counter is not monotonic across a restart.
Hermes already tolerates this by falling back to Postgres.

### Use an ordered version type

The version must stay numerically ordered.
Do not replace it with an opaque token.

CAUTION: an opaque string version breaks ordering.
The strings `"2"` and `"10"` compare in the wrong order.
Hermes compares versions in six places.
A lexicographic order silently drops writes and reorders results.

### The 2^53 boundary

The TypeScript client decodes a version through a safe-integer check.
Versions above 2^53 (9,007,199,254,740,992) make the client throw.

A nanosecond timestamp is about 1.76e18 today.
That value exceeds 2^53 by roughly 195 times.

CAUTION: do not assign a nanosecond timestamp as a version on a scope that the
gateway serves.
One deployed project does exactly this and can break its own web client.

Prefer the `ProjectedRuntimeStore` counter.
Prefer a millisecond timestamp if a clock value is required.

### Source identifiers

A source identifier that ends in a colon activates a per-prefix replay fence.
The fence is bounded by `MaxAppliedEvents` and drops its oldest entries.

A poisoned fence heals on its own.
After `MaxAppliedEvents` other sources, the prefix stops gating events.

Prefer an empty source identifier for writes that carry their own version.

## Time to live

A projection with `TTL` set retires records as they expire.
Retirement reports a delete to every observer.

This matters for two reasons:

1. Aggregates subtract the expired record, so their counts stay exact.
2. Replicas learn to drop the record, so they converge.

An observer that receives a delete must reverse the record's contribution.
An accumulator without that path reports drifting counts.

## Snapshots

The snapshot lane is shadow mode.

Hermes compares a durable artifact against a rebuilt partition.
Hermes does not yet serve reads from the artifact.
The served path always rebuilds.

A snapshot format change therefore degrades to a rebuild.
It does not corrupt a served read.

Store the snapshot format version in the artifact.
Discard an artifact whose version does not match.
One deployed project already uses this pattern for a separate snapshot lane.

## Tenant isolation

Hermes enforces these invariants.
Keep them intact when you change the module.

| Invariant | Where |
| --- | --- |
| The record key embeds the organization | `recordKey` |
| The scope key is a three-part struct | `scopeKey` |
| Every read re-checks the organization | `recordMatches` |
| `organization_id` is never an indexed field | `normalizeIndexedFields` |
| The organization is overwritten from the record | `normalizeRecordDataWithOrganization` |
| An empty organization is rejected on every read | `store.go` read paths |

### Never build a composite key by joining raw strings

Escape the separator, or use a comparable struct.
Hermes uses a struct key for the bitmap index.

CAUTION: a joined key without escaping collides.
The organization `org_1` with record `a|b` produces the same key as the
organization `org_1|a` with record `b`.
On a projection shared by many organizations, one tenant overwrites another.

## Agent checklist

Before you finish a Hermes change, answer each question.

1. Did a public contract change?
2. Which invariant still holds?
3. What evidence proves it: test, benchmark, or static check?
4. Does the fallback path still work?
5. Which scope boundary did the change touch?
6. Does a regression guard cover the change?
7. Does the documentation explain the change?

## Agent prohibitions

Do not perform these actions.

| Prohibition | Reason |
| --- | --- |
| Do not index an identifier field | Retains one bitmap per record |
| Do not skip candidate re-validation | Two read paths would disagree |
| Do not hold a partition lock across a database call | Serializes every apply |
| Do not trust a declared count in a binary artifact | Drives an unbounded allocation |
| Do not replace the numeric version with an opaque token | Breaks last-writer-wins |
| Do not retire a record without notifying observers | Aggregates drift upward |
| Do not exceed `MaxRecords` or `MaxBytes` | Evicts live data |
| Do not expose a projection through the gateway without an audience policy | Leaks owner-private data |

## Measured reference

Measured on an Apple M1 Pro with Go 1.26.
Treat these numbers as a baseline, not a target.

| Operation | Before | After |
| --- | --- | --- |
| Columnar batch, 10,000 rows | 8.62 ms | 4.69 ms |
| Columnar bitmap filter sum | 8.19 ms | 4.71 ms |
| Rebuild, 10,000 records | 22.49 ms | 21.74 ms |
| Apply batch of 64 | 212.7 µs | 193.9 µs |
| Point write | 7.88 µs | 7.63 µs |
| Bitmap index query | 6.59 µs | 6.48 µs |

Four root causes produced those gains.
Each one is documented above.