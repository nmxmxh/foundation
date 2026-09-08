# Projection Read Authorization — Handoff

Status: ACCEPTED and implemented in `server-kit/go/projectiongw` (2026-09-08).
        See "Decision" below for the answers to the open questions and for what
        a consuming project has to do.
Date: 2026-09-08
Raised by: `chowdash_rider_v1` (ChooseChow / ChowDash)
Target: `server-kit/go/projectiongw`, `docs/projection_freshness_contract.md`
Severity: high for any single-tenant deployment generated from this template

## Summary

The projection read path authorizes by **tenant and nothing else**. A subscriber
that is entitled to a scope receives *every record in that scope for its
organization* — both in the snapshot and in the live delta stream.

That is correct for a B2B deployment where the organization is the customer. It
is wrong for the shape this template also generates: a consumer marketplace
where every end user is a member of one organization. There, "tenant" is not an
audience — it is everybody — and the read path becomes a broadcast of every
user's rows to every signed-in device.

No project can fix this from the outside: the fan-out encodes one frame per
scope and shares it across subscribers, so there is no seam where a project
could filter per recipient.

## Evidence (foundation code, read 2026-09-08)

| What | Where |
| --- | --- |
| The tenant is the only trust boundary; the doc comment says so | `server-kit/go/projectiongw/http.go:83` (`SecurityTenantFunc`) |
| Scope identity is `tenant:domain:collection` — no audience term | `server-kit/go/projectiongw/gateway.go:378` (`ScopeKey`) |
| Applied mutations are grouped by scope key only | `server-kit/go/projectiongw/gateway.go:265` (`groupAccepted`) |
| One frame is encoded per scope, then broadcast to every subscriber of that key | `gateway.go:157` (`onApplied`) → `hub.go:152` (`Broadcast`) |
| Subscription is by exact scope; the subscriber carries no identity | `hub.go:113` (`SubscribeWithVectors`) |
| Snapshot query filters on organization only, bounded to the newest 1024 | `gateway.go:16`, `gateway.go:109` |
| Scope-level allowlisting exists, record-level does not | `http.go:67` (`ScopeAllowlist`), used by `discovergw` |

The model already has an audience concept — `ScopeAllowlist` plus
`PublicTenantFunc` is how the anonymous discovery mount is made safe. It stops
at scope granularity. Everything inside a permitted scope is undifferentiated.

## What this means in the generated project

`chowdash_rider_v1` is deliberately single-tenant — one marketplace, one
organization, asserted in code:

```go
// internal/service/user/service.go:111-115
// The organization is the deployment tenant, not a per-user property: one
// marketplace, one org, or customers/chefs/riders cannot see each other.
org := s.defaultOrg   // security.DefaultOrganizationID == "org_default"
```

Every customer, chef and rider therefore authenticates into `org_default`, and
the client subscribes to 36 scopes at sign-in. The rows that reach every signed
in browser include:

| Scope | What the record carries |
| --- | --- |
| `profile/chow_profiles` | `to_jsonb(row)` — display name, **phone, email, city, allergies**, external subject id |
| `message/order_messages` | message bodies of other people's order conversations |
| `message/order_threads` | who is talking to whom, about which order |
| `marketplace/carts` | other customers' cart lines and prices |
| `marketplace/marketplace_orders` | other customers' orders, totals, schedules |
| `wallet/wallet_transactions` | other people's ledger movements |
| `billing/subscriptions` | other people's plan and billing state |

The project's own security profile (`docs/security/profile.md`) classifies these
as High sensitivity with object-level access rules — "Customer, merchant,
support, ops/admin" for orders, "Participant, support, ops/admin" for messages.
The read path does not enforce those rules. The UI filters the rows it renders
(`records.filter(r => r.customer_profile_id === profileId)`), which is a display
decision, not a control: the data has already been delivered to the device.

Scope of exposure: **authenticated users of the same organization**, not the
public. The anonymous `discovergw` mount is allowlisted to
`discovery/discover_chefs` and serves an explicit public field list, so it is
not implicated.

## The second cost: fan-out and client memory

The same missing audience term makes the lane expensive in a way that scales
with the *organization's* activity rather than the user's:

- **Client resident set.** Every client snapshots up to `DefaultSnapshotLimit`
  = 1024 records per scope (`gateway.go:16`) and holds them in a JS store per
  scope. In this project that is ~18 live scopes; at a few hundred bytes per row
  it is megabytes of JSON per tab, on phones, none of which the user can see.
- **Delivery amplification.** One cart edit is broadcast to every subscriber of
  `marketplace/carts` in the org. With *N* signed-in users, org write traffic is
  delivered *N* times, and each recipient discards ~all of it.
- **Drop and resync storms.** The per-subscriber buffer is 256 frames
  (`hub.go:27`); an overflow drops frames, signals `resync`, and the client
  re-snapshots the full 1024 rows per affected scope. Broadcasting everyone's
  writes to everyone makes that overflow a function of org size.
- **A silent correctness cliff.** Because the window is the newest 1024 records
  *of the whole organization*, a user's own rows fall out of it once the org is
  busy. Nothing errors; the screen just shows less than exists. Every "the chef
  can't see the order" / "my message never arrived" report has this shape.

An audience term fixes all four at once: a per-user window is nowhere near 1024
rows, and a user only receives frames addressed to them.

## Proposed design — audience-partitioned scopes

The constraint to respect is the one that makes the hub fast: **encode once,
share the frame**. So the audience must be a *partition key*, not a
per-subscriber filter applied to a shared frame.

1. **Policy per scope.** The gateway takes an `AudiencePolicy` per
   `domain/collection`:
   - `Public` — everyone in the tenant (catalogue, discovery).
   - `Tenant` — today's behavior, kept for B2B deployments.
   - `Audience(fn)` — `fn(record) []string` returns the audience ids a record
     belongs to (e.g. `customer_profile_id`, `merchant_profile_id`, a thread's
     participants).
2. **Audience in the scope key.** `ScopeKey` gains an audience term:
   `tenant:domain:collection:audience`. `groupAccepted` groups mutations by
   (scope, audience), so a record with two audiences is encoded into two groups
   and each subscriber receives it exactly once from its own key. Encoding cost
   stays bounded by *distinct audiences in a batch*, not by subscriber count.
3. **Identity at subscribe time.** A sibling of `TenantFunc`:
   `AudienceFunc(r *http.Request) ([]string, error)`, derived from verified
   claims (in this project: the caller's profile ids). The subscriber is
   registered under its own audience keys. Client-supplied audience values are
   never trusted, exactly as the tenant is not.
4. **Snapshots use the same term.** The `Resolver` adds the audience as a
   `hermes.QueryFilter`. `QueryPlan` conjoins filters, so a record with two
   possible owners needs one indexed audience field rather than an OR: the
   recommendation is that the projection materializes `audience_ids` and hermes
   gains an array-contains filter kind. (Alternative, no hermes change: the
   mirror writes one index record per audience. Costlier, but project-local.)
5. **Fail closed.** A scope declared `Audience` whose audience cannot be
   resolved is a 403 — never a silent fall back to tenant scope. The delta path
   must fail the same way the snapshot does, or the gap simply moves.
6. **Default and migration.** Default stays `Tenant` so existing deployments do
   not change behavior on upgrade. New generated projects should be required to
   declare a policy for every projected scope, with the practice control failing
   on an undeclared one — an omission then reads as a build error rather than as
   an accidental broadcast.

### Complementary lever: field minimization

Independent of audience, a projection should carry only the fields its consumers
render. The pattern already exists in this project for the public feed
(`discoverChefFields`) and generalizes: a per-scope field allowlist in the
projection source. `profile/chow_profiles` is the worst offender today because
it projects `to_jsonb(t)` — the whole row, PII included — to satisfy consumers
that only ever read `display_name` and `avatar_url`.

## Contract and evidence changes

- `docs/projection_freshness_contract.md` requires eight fields of every
  projection note. All eight are about freshness; none asks **who may read
  this**. Add: *audience* (public / tenant / per-record), *authorization
  enforcement point*, and *field allowlist*. A projection note that cannot name
  its audience is not reviewable today.
- `docs/security_practices.md` does not mention projections at all. The read
  path is the one place in the stack where authorization is decided by a wire
  scope rather than by a service, and it should say so.
- TLA: `docs/specs/tla/FrontendLiveProjection.tla` models the client's live
  loop. The invariant to add alongside it: *no record is delivered to a
  subscriber outside its audience* — the delete/resync lanes included, since a
  tombstone leaks membership.
- Test evidence expected with the change:
  1. two subscribers on one scope with disjoint audiences — neither observes the
     other's records, on snapshot **and** on delta;
  2. a record with two audiences is delivered once to each, not twice to either;
  3. an unresolvable audience on an `Audience` scope is refused, not degraded;
  4. a benchmark showing encode cost still scales with distinct audiences per
     batch, not with subscriber count.

## Interim mitigations available to a project today

Neither of these closes the hole; they reduce the blast radius while the
foundation change lands.

1. **Field allowlist on sensitive scopes.** For `profile/chow_profiles`, project
   `id, organization_id, display_name, avatar_url, status` and drop phone,
   email, city, allergies and the external subject id. Verified consumers in
   `chowdash_rider_v1`: the chef directory and the ops console read only
   `display_name`/`avatar_url`; the eater's own allergies would move to the
   authorized `profile:get_profile` read; the chef's customer-phone fallback is
   already redundant with the authorized `marketplace:get_order`.
2. **Move genuinely private reads off the projection** and onto authorized
   commands (this project has already done it for order transcripts via
   `message:list_order_messages`), accepting the loss of live push for them.

## Decision (2026-09-08)

Accepted as designed, with the four open questions answered below. Shipped in
`server-kit/go/projectiongw/audience.go` plus changes to `gateway.go`, `hub.go`
and `http.go`; evidence in `audience_test.go` and `audience_bench_test.go`.

**1. `Audience` vs an opaque `PartitionFunc` — the distinction dissolved.** The
primitive is `RecordAudienceFunc(mutation) []string` on the record side and
`SubjectAudienceFunc(request) []string` on the subscriber side. Nothing in the
gateway interprets an audience id, so keying on a region, kitchen or delivery
zone is already expressible: it is an opaque partition function wearing the name
the security property is stated in. Naming it `Audience` keeps that property
readable at the call site, which is worth more than a more general-sounding name
for the same mechanism.

**2. `Tenant` stays the default, and `Strict` is available now.** Changing the
default would silently alter delivery for every existing B2B deployment on
upgrade — the wrong failure direction for a change whose whole point is not
surprising people. Instead `AudienceConfig.Strict` refuses any scope with no
declared policy (`ErrAudienceUndeclared`), so a project turns silent-broadcast-
by-default into a wiring error the moment it has finished declaring its scopes.
The next major can flip the default; the lever exists today and does not need to
wait for it.

**3. No hermes change. Audience indexing stays a declared field.** The gateway
runs one bounded read per (declared field, audience id) pair and unions them by
version, so "customer OR merchant" needs no OR in the query planner: a capped
number of O(limit) indexed reads is still O(limit). `MaxAudienceSnapshotReads`
(32) keeps identity breadth from turning into scan cost. Measured on an M1 Pro
over a 4096-record scope at limit 128: 89µs at one audience, 357µs at four,
1.6ms at sixteen — linear in pairs, as intended. An array-contains filter kind
in hermes remains the right optimization for a record with many audiences; it is
now an optimization rather than a prerequisite, which is what made this landable
without touching the query planner.

**4. The tombstone carries the audience, or the delete does not converge live.**
The gateway observes applies after the record is gone, so it cannot recover a
pre-delete audience without keeping a record→audience map — unbounded state on
the apply path, and still wrong for a record upserted before process start. The
alternatives were: broadcast tombstones tenant-wide (leaks membership, which is
the bug), or fail closed. It fails closed: an undeliverable mutation is dropped
and counted in `Gateway.AudienceDrops()`, and the client reconciles the deletion
on its next snapshot. Because the gap is counted rather than silent, a
deployment can alarm on it. **A per-record scope should emit deletes carrying
its audience fields**; then deletions converge live like any other mutation.

### What a consuming project does

1. Declare a policy per scope whose records belong to particular parties:
   `WithAudience(AudienceConfig{Policies: {"marketplace/orders": {Mode:
   AudiencePerRecord, Fields: []string{"customer_profile_id",
   "merchant_profile_id"}}}})`. Declare those same fields in the projection's
   `IndexedFields`, or snapshots stay correct but pay a scan.
2. Supply `HandlerConfig.Audience` — the caller's profile ids from verified
   claims. `SecuritySubjectAudienceFunc` is the default for records that name
   the authenticated subject directly.
3. Make deletes carry the audience fields.
4. Set `AudienceConfig.Strict` once every scope is declared.
5. Independently, cut the projections' field lists down to what consumers
   render. Audience decides who; the field list decides what. `to_jsonb(t)`
   fails the second test even when the first passes.

Nothing in the interim mitigations below is obsolete: field minimization is
complementary, and moving a genuinely private read onto an authorized command
remains right for data with no safe audience partition.

## Open questions — resolved

1. ~~Is `Audience` the right primitive, or should the gateway take an opaque
   `PartitionFunc`?~~ Resolved: it is one, see Decision 1.
2. ~~Should `Tenant` remain the default?~~ Yes, with `Strict` as the opt-in, see
   Decision 2.
3. ~~Does hermes want an array-contains filter kind?~~ Not needed to land this;
   a later optimization, see Decision 3.
4. ~~Should the tombstone carry the pre-deletion audience?~~ Yes, and the
   project must emit it; the gateway fails closed and counts, see Decision 4.
