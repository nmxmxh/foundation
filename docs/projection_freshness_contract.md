# Projection Freshness Contract

Status: mandatory for Hermes, search, read-model, Redis-cache, and materialized-view changes
Owner: Data Plane Architecture

## Purpose

Foundation projects use durable state, Hermes projections, Redis, search lanes,
materialized views, and frontend stores. Agents must not treat these as the
same consistency model. This contract names freshness semantics and required
evidence for projection-like reads.

## Freshness Modes

| Mode | Meaning | Required evidence |
| --- | --- | --- |
| `source-of-truth` | Read is from durable authoritative state. | transaction or query test |
| `read-your-write` | Caller can fence reads to observe its own committed write. | fence token or epoch test |
| `monotonic-read` | A reader never observes an older projection than it already saw. | watermark or session test |
| `bounded-stale` | Projection may lag within an explicit window. | lag metric and threshold |
| `stale-while-revalidate` | Stale data is acceptable while refresh is active. | refresh/fallback test |
| `fallback-required` | Projection failure must route to a safer lane. | degradation and fallback test |

## Required Fields For Projection Notes

Every projection, cache, or read-model design note must state:

1. source of truth
2. freshness mode
3. source watermark or epoch
4. maximum stale window, if any
5. replay and rebuild path
6. drift detection strategy
7. fallback lane
8. user-visible behavior during degradation

## Drift Repair Algorithms

Use the lightest algorithm that proves the required property:

1. count parity for cheap completeness checks
2. watermark fences for ordered replay
3. Merkle or hash sampling for large read models
4. full rebuild for irreversible drift
5. fallback to source-of-truth when the projection cannot prove freshness

## Agent Review Checklist

- [ ] Does the code distinguish durable truth from projection state?
- [ ] Is stale data user-visible, and is that acceptable for the product lane?
- [ ] Is there a metric for projection lag or fallback count?
- [ ] Can rebuild restart without corrupting visible state?
- [ ] Does the test suite prove the chosen freshness mode?
