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
9. **audience** — who may read a record of this projection
10. **authorization enforcement point** — where that audience is enforced
11. **field allowlist** — which fields the projection materializes

Fields 1-8 are all about freshness. A projection note that answers only those
is not reviewable, because the read path decides authorization by a wire scope
rather than by a service: nothing downstream of it re-checks who may see a
record. Fields 9-11 are what make that decision visible in review.

## Audience

The read path's default trust boundary is the tenant. That is correct when the
organization IS the customer, and wrong when one organization holds many
mutually-untrusting end users — there "tenant" is not an audience, it is
everybody, and every subscriber receives every row of the scopes it binds.

| Audience | Meaning | Required evidence |
| --- | --- | --- |
| `public` | Deliberately published to anonymous readers. | scope allowlist plus a materialized public field list |
| `tenant` | Every authenticated member of the organization. | a statement that org membership IS the access rule for these records |
| `per-record` | Only the parties a record names. | `projectiongw.AudiencePolicy` on the scope, plus disjoint-subscriber tests on snapshot AND delta |

Declaring `tenant` is a claim, not a default: it asserts that any member of the
organization may read any record in the scope. If the product's own access
rules say otherwise for that data (an order's parties, a message's
participants, a person's profile), the scope is `per-record` and needs a policy.

### Control PROJFRESH-02

`tooling/scripts/projection_audience_check.sh` reports a project that uses the
projection gateway without declaring audience policies. Generated projects run
it as `make check-projection-audience`, and `lint-foundation` includes it.

The check warns rather than fails when no policy is declared, because
tenant-wide delivery is a legitimate posture for a business-to-business
deployment. It fails on wiring that cannot work: an `AudiencePerRecord` scope
with no `HandlerConfig.Audience` resolver answers 403 to every read.

### Enforcement point

Name where the audience is actually enforced, and prefer the one place that
covers both halves of the read path:

- **`projectiongw` audience policy** — partitions the delta fan-out and filters
  and re-checks the snapshot. This is the only enforcement point that holds for
  live push.
- **Authorized command** — the read leaves the projection lane entirely. Correct
  for data with no safe audience partition, at the cost of live push.
- **Client-side filtering is NOT an enforcement point.** Rows are on the wire
  and in the device's memory before any client code runs. A per-record filter in
  a UI is a display decision; record it as such and name the real control.

### Field allowlist

Independent of audience, a projection carries only the fields its consumers
render. A projection source that materializes whole rows (`to_jsonb(t)`) hands
every subscriber every column, PII included, to satisfy consumers that read two
of them. What was never materialized cannot leak, so the projection definition
is the allowlist — the same discipline as an explicit `SELECT` list.

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
- [ ] Does every projected scope name its audience, and does that audience match
      the product's own access rules for that data?
- [ ] For a `per-record` scope: is the policy enforced on the snapshot AND the
      delta stream, with a test showing two disjoint subscribers observing none
      of each other's records on both?
- [ ] Does an unresolvable audience refuse the read, rather than falling back to
      tenant scope?
- [ ] Do deletes in a `per-record` scope carry the audience fields, so a
      deletion converges live instead of waiting for the next snapshot?
      (`DeleteRecordWithFields` / `AddDeleteRecordSource`, not `DeleteRecord` /
      `AddDeleteSource` — and the audience fields only, never a copy of the row.)
- [ ] Does the projection materialize only the fields its consumers render?
