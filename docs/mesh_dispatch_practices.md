# Mesh Dispatch Practices

Status: baseline
Date: 2026-08-22
Inspiration: `inos_v1/docs/P2P_MESH.md` (the INOS RTC mesh). Foundation adopts its topology and economics; every mechanism below is a measurable improvement over the heuristic it replaces.

## Purpose

This document answers three operational questions for compute placement across nodes:

1. How do nodes connect?
2. How is load optimized?
3. How is the class of an edge decided?

It also names the update formulas so operators can reason about convergence without reading implementations.

## Node connection lifecycle

Foundation and INOS share one topology shape with two transports:

| Stage | INOS RTC mesh | Foundation today |
| --- | --- | --- |
| First contact | Known seed domain or peer list | Redis URL / WS endpoint in config |
| Transport | WebRTC datachannels + gossip signaling | RedisBus pub/sub + WS routes + gRPC frames |
| Discovery | DHT + gossip topics (`webrtc.signaling`) | `wsroute:*` maps; placement mirror channel |
| Steady state | Fully decentralized gossip | Same payloads over the bus lanes below |

The dispatch layer is transport-agnostic on purpose. A lane-mirror frame is bytes on a channel; today that channel is `compute:lane:v1` on the Redis bus, and the same frame rides INOS gossip without redesign. Bootstrap remains a distributed-systems constraint in both stacks — first contact needs one known address, never central control.

Connection procedure for a new executor node:

1. Attach to the region transport (bus client or WS route).
2. Open or create the region's dispatch block (`runtimehost.OpenDispatchRegion`; Rust hosts via `ovrt_dispatch::DispatchBlock::open`). Exactly one process per region publishes membership.
3. Publish local lanes: descriptor rows once at startup and on capability change; stat rows continuously through sampling.
4. Subscribe to the mirror channel and apply inbound reports into locally owned mirror rows.
5. Decide locally. Placement never blocks on the network: the hot path reads shared memory only.

## Hub topology

The seed/hub/edge hierarchy carries over directly:

| Role | Owns | Publishes | Consumes |
| --- | --- | --- | --- |
| Seed | Authoritative region tables | Every class it runs | Mirrors from peers |
| Hub | Its region's block; aggregates edges | One coalesced batch per region per window | Edge updates, peer hub mirrors |
| Edge | Mirror rows only | Its own sampled lanes, on change | Hub mirrors |

Rules:

1. One publisher per block. The flip-index protocol assumes it; two publishers is a configuration bug, not a race to handle.
2. Hubs never merge tables. A lane belongs to exactly one region's table; cross-region work always crosses a transport hop whose round-trip latency lands in the mirrored EWMA.
3. Edges stay stateless about peers. An edge that loses the bus keeps serving local work and refreshes mirrors when reconnected — missed updates degrade convergence speed, never correctness.

## Load optimization

Placement is argmin over measured expected latency:

```text
expected_ns(lane) = ewma_ns x (1 + inflight / max_concurrency)
score(lane)       = expected_ns - affinityBonus(if locality key hits)
select            = argmin score, subject to hard filters
```

Where each ingredient comes from:

| Signal | Source | Replaces |
| --- | --- | --- |
| `ewma_ns` | Owner-sampled completions, α = 1/8 | "lowest-latency holder" heuristics guessed from history |
| `inflight / max` | Single-writer atomic row | Polling load dashboards |
| affinity bonus | Bloom hit over locality keys (partition/scope/CIDR-region) | Static region pinning |
| stale window | `lastTickSeen` vs global click, STALE_TICKS = 4 | Lease heartbeats and TTL registries |

INOS' conservative load targeting (25–50% sweet spot, 50% ceiling) falls out of the queueing term instead of a policy constant: a lane at `inflight = max` doubles its own expected latency and loses selection before any external throttle fires. This is the same discipline as the SAB `InboxCount` backpressure counter, moved next to the data it protects.

Update formulas:

1. **Local sampling**: on completion, `ewma ← α·sample + (1−α)·ewma`, heartbeat stamped from a fresh click. Failed runs sample too; hiding failures lets a degrading lane keep winning.
2. **Mirror publish**: batched per node, on material change or once per tick window. Coalescing is the publisher's job.
3. **Mirror apply**: overwrite the local mirror row word-by-word with release stores. The heartbeat is re-stamped with the LOCAL click — cross-region clicks are independent counters, so arrival itself is the liveness proof and freshness decays on our clock alone. No reconciliation protocol: the next report supersedes this one.
4. **Exclusion**: unsampled (`ewma = 0`), stale (`age > STALE_TICKS`), retired (empty mask), jurisdiction-mismatched, deadline-infeasible lanes select nothing — in that order, fail closed.
5. **Mirror trust**: a lying publisher can only poison its own rows' numbers, never memory it does not own. Two guards bound the damage at apply time: latency claims below `MinPlausibleEwmaNs` are refused loudly, and publisher ticks are discarded entirely. Stronger attribution (signed frames over mTLS transport) belongs to the transport lane; first-party sampling remains the ultimate correction.

## Edge class decision

Class answers "what may run here", not "what runs here":

1. **Capability class**: `unit_class_mask` bitmask checked before scoring. A GPU lane simply does not cover a scalar request.
2. **Jurisdiction class**: exact-match-or-global pre-filter, fail closed. Comes from tenant metadata at registration — never from IP geometry. The CIDR table (`runtimeconfig.EdgeRegionTable`) supplies the soft geometric prior only: closeness seeds the affinity key, measurement corrects it.
3. **Trust class**: trust tiers ride the same mask namespace; sandboxed work cannot claim trusted lanes.
4. **Topology class** (`LaneClass`): seeds/hubs/edges differ in what they publish, not in how they are scored. An edge lane beats a hub lane whenever it is genuinely faster for the byte's journey.

The near-data rule from both stacks applies unchanged: the affinity term makes "run where the projection lives" the default, and `RemoteComputeTicket` binds one checksummed chunk to one placement decision so storage location drives compute location.

## Invariants and conformance

Mapped in `docs/specs/tla/conformance.tsv` under `DispatchPlacement`, each anchored to real code:

1. `TickMonotonic` — the click never moves backwards.
2. `JurisdictionFailClosed` — served implies exact-or-global match.
3. `StaleLaneExcluded` — served implies fresh heartbeat.
4. `RetiredSlotExcluded` — empty-mask slots never serve.

## Relationship to INOS mechanisms

| INOS primitive | Foundation counterpart | Improvement |
| --- | --- | --- |
| ChunkAwareLoadBalancer routes by chunkLocations + lowest latency | Dispatch argmin over sampled EWMA + Bloom affinity | Measured, ns-class, no polling; jurisdiction/trust are hard filters |
| AdaptiveAllocator replica sizing by size/demand/budget | Lane count stays fixed; demand expresses itself as inflight pressure and new lanes | No allocator tuning; saturation self-prices via the queueing factor |
| Lamport timestamps, no global clock | Region-scoped click advanced per decision; mirrors carry region-local ticks | Causality where it matters, no cross-region clock agreement needed |
| Merkle anti-entropy over chunk sets | Generation-stamped dual-buffer swaps; mirrors supersede, TTLs bound drift | O(1) publication instead of tree walks for capacity data |
| InboxCount backpressure pauses network reads | inflight/max queueing factor excludes saturated lanes | Backpressure integrated into selection instead of adjacent to it |
| Credit-tier budget multipliers | Future: priority classes map onto deadline budgets | Reserved; placement math already accepts deadlines |

## Review checklist

- [ ] New write path states which lanes it stales and where the mirror publish happens.
- [ ] Every hook installed on a kernel seam is paired with a staleness uninstall.
- [ ] Jurisdiction values come from tenant metadata, never from network geometry.
- [ ] New executors register a class bit and sample completions through `SampledUnit`.
