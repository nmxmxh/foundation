# Foundation Quick Start

Status: 0.0.1
Date: 2026-06-28
Owner: Platform Architecture

## Purpose

This is the minimum viable understanding path for a developer, reviewer, or
agent who needs to make a Foundation change without reading the entire
documentation set first.

For concept definitions and pre-answered questions, see `foundation_glossary.md`.

Use this file to choose the first lane. Then read the owning contract before
editing.

## Fifteen-Minute Path

Read these in order:

1. `foundation_tour.md`: one request through Foundation.
2. `foundation_architecture_contract.md`: platform, scaffold, and project
   ownership boundaries.
3. `foundation_nervous_system.md`: command, event, worker, projection, and
   realtime lifecycle.
4. `agent_operating_contract.md`: evidence and handoff requirements.
5. `practice_controls.md`: which rule is enforced by which check.

After that, read only the lane document for the files you are changing:

- Backend/domain: `coding_practices.md`, `database_practices.md`,
  `security_practices.md`, and `testing_practices.md`.
- Runtime/performance: `runtime_foundation.md`, `performance_practices.md`,
  `performance_lab.md`, and the relevant Rust/GPU/native practice doc.
- Scaffold/template: `scaffold_manifest.md`,
  `foundation_architecture_contract.md`, and `tooling/docs/enforcement.md`.
- Frontend/UI: `frontend_scaffold_sync.md`,
  `styling_design_practices.md`, and `testing_practices.md`.

## Critical First Questions

Before editing, answer:

1. Which layer owns this file: platform module, managed scaffold, or
   project-owned code?
2. Which invariant could break: tenant isolation, correlation, idempotency,
   lifecycle events, bounded work, freshness, authorization, or payload shape?
3. Which check or test will fail if this mistake comes back?
4. Which fallback remains when the fast path, cache, projection, worker,
   external dependency, or optimized lane fails?

If any answer is unclear, stop and read the owning document before changing
code.

## Common High-Impact Mistakes

- A typed or binary refactor improves one lane but regresses JSON compatibility,
  HTTP ingress, or event decode. Benchmark every active lane before claiming a
  performance win.
- A `graceful` event emit path accepts generic payloads on a hot lane instead
  of passing an `extension.Object`, typed protobuf, or explicit owned-object
  fast path.
- A registry route is added without `HTTPRoute.Validate()` coverage, or an
  unknown event type is silently ignored without a metric/debug signal.
- A scaffold change updates templates without updating generated-project
  checks, docs, and `templates/scaffold.manifest.tsv`.
- A service trusts client-supplied tenant or organization data instead of
  deriving scope from authenticated context.
- A mutating command loses `correlationId`, idempotency key, or terminal
  requested/success/failed lifecycle event.
- An optimized Hermes, cache, GPU, WASM, FFI, or direct-dispatch path bypasses
  the canonical lifecycle instead of refining it.

## Minimum Evidence

Every non-trivial change needs one durable proof:

- static check output for mechanical rules
- unit/integration/contract test output for behavior
- benchmark/profile output for performance claims
- query plan or migration proof for persistence changes
- review note for human-only controls

Use the seven-question Definition of Done in
`agent_operating_contract.md` for architecture-sensitive changes.
