# Handover Note: Codex

**Project**: Ovasabi Foundation & Cognitive Wire (CW)  
**Architect**: Sovereign Architect (Abuja, Nigeria)  
**Status**: Stealth / High-Performance Runtime  

## 1. History & Context
Ovasabi Studios is an Abuja-based engineering lab focused on technical sovereignty and hyper-performance. The ecosystem evolved from `fintech_v1` (ledger/sensor reference) and `inos_v1` (distributed mesh research) into a unified, agent-optimized **Foundation**.

## 2. Core Achievements
- **16ns Dispatch**: Zero-allocation same-process communication.
- **Zero-Copy Pipeline**: `SAB -> Rust (Compute) -> Go (Logic) -> TS (Render)` using SharedArrayBuffer and atomic signaling.
- **Performance Ladder**: Automated transport detection (`ffi -> shm -> stdio -> ws -> http`).
- **CWF (Cognitive Wire Format)**: A token-efficient, pipe-delimited serialization format designed for AI context exchange.

## 3. The Shift: Cognitive Wire (CW)
We are moving away from traditional "Centralized API" models towards **Shared AI Compute.**
- **Stealth Extension**: CW is not a new layer; it is an extension of the Foundation's existing transport.
- **Redis-Local State**: Redis may be sharded/replicated close to the sender for ephemeral coordination, recent claim exchange, connection routing, and short replay windows.
- **Durable Truth Boundary**: PostgreSQL remains the authoritative system of record for verdicts, audits, manifests, billing/economy settlement, model promotion, and long-lived lineage.
- **Binary-First CWF**: Optimizing the text-based CWF spec into Protobuf/Cap'n Proto-backed binary-wire contracts for the "hot-path."

## 4. Architectural Pillars
- **Adversarial Consensus**: Multi-model debate loops where verified claims survive and hallucinations are killed early.
- **Geomantic Routing (Odù)**: Compact binary routing keys for routing queries to specialized knowledge shards and model capabilities.
- **Agent-Optimized**: Blueprints are "Machine-Readable Instructions." The system is maintained by AI Agents under the Sovereign Architect's supervision.

## 5. The Plan (Immediate Action)
1. **Extend Foundation**: Implement CW as a narrow "stealth" hot-path capability inside `runtime-transport` and `runtime-sdk`, not as a separate platform.
2. **Shard Ephemeral State**: Replicate Redis state locally/close to sender for connection routing, claim/attack buffers, recent verification events, presence, subscriptions, and replay/idempotency windows.
3. **CWF Optimization**: Promote CWF to schema-backed binary contracts for high-volume `InferenceRequest`, `Candidate`, `Claim`, `Attack`, `Verify`, and `Verdict` exchange.

Out of current focus: standalone product rollout should wait until the CW frame contracts and local bounded orchestration path are proven.

## 6. Later Plan
- **Model Bootstrap**: Start with a builder/coding open model class for implementation and a reasoning/critic model class for attack/verdict work. Add smaller extractor and embedding models only after the frame contracts are stable.
- **DB Distribution**: Postpone durable DB sharding or multi-region writes until measured traffic proves the need and conflict resolution is explicitly designed.
- **Mesh/Economy/Population**: Treat mesh inference, economy settlement, and open-model population management as later phases after local bounded orchestration works end to end.

## 7. Strategic Intent
The goal is **Technical Sovereignty.** By combining low-cost bare-metal (Hetzner), self-hosted orchestration (Coolify), and the CW engine, the architect wields the power of a global software firm from a single room in Abuja.

**Codex Instructions**: Maintain the "Zero-Allocation" discipline. Every byte matters. Every nanosecond is a battleground. Follow the "Golden Path" blueprints exactly.
