# Cognitive Wire (CW) Practices

Status: v1  
Date: 2026-05-01  
Owner: Platform Architecture

## 1. The Cognitive Wire (CW) Vision

Cognitive Wire is a **stealth extension** of the Foundation architecture. It moves AI compute and state delivery "close to the sender," creating a truly serverless, low-latency intelligence layer.
CW is not a new platform layer, service mesh, or alternate event bus.
It begins as a narrow Foundation hot-path capability for high-volume AI claim exchange.

### 1.1 Shared AI Compute
Compute is not locked on the server. The **Foundation Runtime** distributes inference and verification tasks between the client (Browser WASM) and the server (Native Rust) over the **Cognitive Wire.**
- **Local-First**: Execute light inference and initial verification on the client.
- **Server-Supported**: Delegate heavy computation or global knowledge retrieval to the server via **CWF (Cognitive Wire Format).**

### 1.2 Binary-Optimized CWF
While CWF began as a text format for token efficiency, the **Foundation Hot-Path** uses a binary-optimized version for:
- **Claim Extraction**: Moving structured assertions at memory speed.
- **Adversarial Consensus**: Rapid "Attack/Verify" rounds between model units.
- **Epoch Signaling**: Coordinating AI state transitions across the wire without JSON overhead.

Execution contracts should use existing schema tooling.
Protobuf or Cap'n Proto definitions are the source of truth for generated Go, Rust, and TypeScript readers/writers.
Text CWF remains valid for prompts, logs, debugging, and human/agent context exchange.
Binary CWF is the runtime format for the hot path.

## 2. Edge-Native State Distribution

To achieve "close to sender" delivery, CW may replicate ephemeral coordination state closer to the user.

### 2.1 Redis-Only Edge State
- **Local Ephemeral Replication**: Redis may hold hot coordination state on the local node or nearest edge hub.
- **Allowed State**: Connection routing, claim/attack exchange buffers, recent verification events, presence, subscriptions, short replay windows, idempotency windows, and route/model warm-state hints.
- **Durable Boundary**: PostgreSQL remains the durable source of truth for verdicts, audit records, manifests, billing/economy settlement, model promotion state, and long-lived lineage.
- **No Early Distributed Writes**: Do not introduce multi-region durable writes or DB sharding until measured traffic proves the requirement and the conflict model is explicit.
- **Source of Truth**: Edge Redis accelerates delivery; it does not become the authority for accepted truth.

## 3. The AI Orchestra (Refined)

The orchestra now operates over the Cognitive Wire:
- **Geomantic Routing (Odù)**: Uses compact binary routing keys to route queries to specialized knowledge shards and model capabilities.
- **Adversarial Consensus**: Debate rounds happen over CWF binary envelopes, ensuring that only verified claims survive to the UI.
- **Narrow Frame Set**: The first CW frame family is limited to inference request, candidate, claim, attack, verify, and verdict exchange.

Initial CW frames:

1. `InferenceRequestFrame`
2. `CandidateFrame`
3. `ClaimFrame`
4. `AttackFrame`
5. `VerifyFrame`
6. `VerdictFrame`

## 4. Operational Discipline

1. **Fastest Path First**: Always attempt local WASM inference/verification before escalating to the server.
2. **Size Out**: Design payloads to "size out" of the network—if data is too large, move it through the **SharedArena** or stream chunks.
3. **Stealth Integration**: CW capabilities should be transparently available via the `foundation/runtime-sdk` and `foundation/runtime-transport` without adding new architectural layers.
4. **No Overhead**: Maintain the "Zero-Allocation" and "Zero-Waste" discipline. AI computation must not bloat the transport.
5. **Schema First**: Hot-path payloads must use generated schema readers/writers, not pipe parsing or dynamic maps.
6. **References Over Bulk Text**: Large outputs, evidence bodies, logs, and artifacts should move behind references; CW frames carry bounded control data.

## 5. Later Research Tracks

### 5.1 Route Class Hints
Odù-inspired route classes may be carried as compact metadata inside CW frames.
They are routing and scoring hints only; they must not determine truth or bypass verification.

Route-class rules:

1. keep the initial class space bounded,
2. version classifier, class table, and fitness profile together,
3. record entropy seeds for replay,
4. fall back to generic routing when confidence is low,
5. measure quality, cost, latency, and verifier success against a no-class baseline.

### 5.2 Quantized Vector References
TurboQuant-style work suggests a later CW extension for compressed vector references.
This should remain outside the immediate frame contract until benchmarks prove value.

Potential uses:

1. KV-cache compression for long-context local runners,
2. compressed vector-search indices for evidence and prior verdict retrieval,
3. compact SharedArena summaries of high-dimensional state,
4. lower-bandwidth transfer of trusted intermediate vectors.

Promotion gates:

1. no material retrieval-recall loss,
2. no measurable increase in hallucination or verifier failure,
3. reproducible decode/scoring across supported runners,
4. clear fallback to uncompressed or simpler quantized vectors,
5. memory or latency win under realistic workloads.
