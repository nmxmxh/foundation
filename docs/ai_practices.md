# AI Runtime Practices

Status: baseline
Date: 2026-05-13
Owner: Platform Architecture

## Purpose

This document keeps AI-related runtime work inside the existing Foundation
contract. AI features are product capabilities, not a separate platform layer,
transport fabric, or event lifecycle.

AI execution must refine the same substrate described in
`docs/foundation_nervous_system.md`: typed contracts, trusted metadata,
tenant scope, idempotency, bounded work, observable terminal states, and
frontend projections.

For AI coding agents, use `docs/agent_operating_contract.md` as the required
workflow. The agent contract defines evidence requirements, handoff shape,
tool-use safety, and the rule that model output, tool output, retrieved text,
and generated code remain untrusted until validated by the owning domain.
Use `docs/ai_threat_model.md` for prompt injection, tool-output poisoning,
memory poisoning, generated-code provenance, and unsafe tool boundary review.

## Contract

1. Use generated Protobuf or Cap'n Proto schemas for AI request, result,
   evidence, scoring, and verdict payloads.
2. Keep prompt text, model responses, embeddings, logs, and evidence bodies out
   of hot control frames. Move large bodies behind object-store references,
   shared arena descriptors, or explicit streams with backpressure.
3. Preserve correlation ID, idempotency key, user, session, organization,
   schema version, locale, and trace fields through AI workers and runtime lanes.
4. Treat model output, tool output, retrieved documents, partner responses, and
   generated code as untrusted input until validated by the owning domain.
5. Treat agent memory, MCP/tool responses, package install scripts, web pages,
   and copied snippets as untrusted until source-attributed or locally verified.
6. PostgreSQL remains the durable source of truth for accepted decisions,
   audit records, manifests, billing/economy settlement, model promotion state,
   and long-lived lineage.
7. Redis may hold ephemeral routing, presence, short replay windows, bounded
   idempotency windows, and low-latency coordination state. Redis is never the
   authority for accepted truth.

## Runtime Lanes

1. Keep small control payloads on direct/binary/runtime-buffer lanes.
2. Use `RuntimeSharedArena`, transferable buffers, object references, or stream
   chunks for large evidence and model-output payloads.
3. Use browser WASM only for bounded local work that preserves the product
   security model and does not expose secrets or privileged tenant data.
4. Use native Rust/FFI/shared-memory lanes for deterministic batched scoring,
   canonical hashing, verification kernels, and parity-sensitive compute.
5. Use Go/server-kit for orchestration, authorization, policy checks, database
   transactions, worker scheduling, and durable audit writes.
6. JSON text is allowed at human/debug boundaries. Hot runtime paths must keep
   typed or binary payloads until the owning handler validates and decodes them.

## Intelligence Graph Lane

1. Foundation intelligence signals are generated at the registry dispatch
   boundary, before app handlers run. This is the central injection point where
   event type, trusted metadata, payload keys, tags, categories, source refs, and
   actor/entity references are normalized once.
2. The default lane is intentionally lightweight: bounded keyword extraction,
   safe metadata tags, graph provenance, actor/entity edge hints, and a small
   sparse hashed relevance vector. It does not call a model, allocate large
   embeddings, or decode typed payloads twice.
3. Durable graph/vector storage is a sink concern. The registry can emit
   `intelligence.Signal` to an async observer that writes to Postgres/pgvector,
   Neo4j, a columnar lane, or a worker queue without coupling service handlers
   to that backend. Observer delivery must be bounded and drop under pressure;
   command dispatch must never wait on analyst-facing graph/vector sinks.
4. Use tags for low-cardinality semantic indexes. Use `knowledge_graph` for
   graph scope, `source_ref` for provenance, `attributes` for bounded graph
   facts, and explicit schema columns for high-cardinality identifiers.
5. Retrieval should be hybrid when promoted: lexical keyword match, vector
   similarity, graph neighborhood expansion, and authorization/tenant filters
   must be composed before results influence user-visible or model-visible
   answers.

## Verification

1. AI mutating flows must emit the same `requested -> success/failed` lifecycle
   as other Foundation commands.
2. Each accepted AI workflow must define bounded retries, maximum payload size,
   maximum tool/model calls, timeout budget, and failure class.
3. Model and tool calls require negative tests for malformed output, oversized
   output, missing citations or evidence references where required, timeout,
   cancellation, and tenant-scope mismatch.
4. Verification/scoring lanes must include parity tests across every lane the
   product actually uses.
5. Benchmarks must report payload size, copy budget, allocation budget, p95/p99,
   error rate, and fallback behavior before a runtime lane becomes a default.
6. Agent-generated code that touches security, persistence, runtime lanes, or
   scaffold defaults must include an evidence ledger entry: tests, benchmark,
   trace, query plan, capture, or explicit reviewed exception.

## Research Tracks

The following ideas remain research until benchmarked and tied to a product
contract:

1. compact route-class hints for model or tool selection;
2. quantized vector references for memory/bandwidth reduction;
3. local-first verification for low-risk, bounded inference tasks;
4. multi-model critique or consensus flows.

Promotion gates:

1. no material retrieval-recall loss,
2. no measurable increase in hallucination or verifier failure,
3. reproducible decode/scoring across supported runners,
4. clear fallback to uncompressed or simpler quantized vectors,
5. memory or latency win under realistic workloads.
