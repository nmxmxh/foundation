# Enforcement

The tooling layer exists to turn blueprint practices into repeatable checks.

It does not replace app-specific CI, but it sets the baseline for:

1. coding practices
2. database practices
3. redis practices
4. migration structure
5. contract drift
6. agent operating contracts and evidence ledgers
7. delivery metrics and operational scaffold posture

Contract drift is expected to fail hard when:

1. source protobuf or Cap'n Proto contracts are newer than generated bindings
2. generated Go, TypeScript, and Rust artifacts are missing for a shared contract family
3. shared schema/version gates diverge across foundation modules

Recommended app usage:

1. extend the shared ESLint, GolangCI-Lint, and Rust configs from this repo
2. call the shell checks from app `make lint` or CI workflows
3. keep app-specific checks close to the app and leave cross-app rules here

## Lint strictness model

Foundation uses a strict-core lint model:

1. foundation/runtime/server code fails on resource-management and communication-contract drift
2. generated projects inherit the same Go, Rust, scaffold, and CP checks through `.golangci.yml`, `clippy.toml`, `rustfmt.toml`, and `scripts/checks/*`
3. frontend scaffolds use ESLint warnings for high-verbosity React shape rules and errors for boundary violations such as raw foundation source imports
4. custom CP shell checks enforce cross-language architecture rules that stock linters cannot see consistently
5. `agent_contract_check.sh` verifies that generated projects carry the
   architect/agent read order, Definition of Done, research ledger, and
   scaffolded self-check entry point
6. `practice_controls_check.sh` verifies that `tooling/practice_controls.psv`
   maps every `CP-*` and `TE-*` heading plus cross-cutting controls to owning
   docs, automation class, evidence, merge-gate posture, and valid script
   references

Native tool mapping:

1. Go: `golangci-lint` owns unchecked errors, context use, body closing, security scans, static analysis, complexity, and allocation hints.
2. Rust: `cargo fmt` and `cargo clippy -D warnings` own formatting, unwrap/expect/panic discipline, and warning-free runtime code.
3. TypeScript/React: ESLint owns React hooks, import boundaries, observer exceptions, blocking atomics, and app-local raw WebSocket construction. TypeScript owns generated contract shape through `typecheck`.
4. CP scripts own foundation-specific communication and performance rules: no oversized runtime control buffer, no hot-path dynamic JSON envelopes in foundation runtime lanes, no compatibility gRPC envelope as a default, no app-internal JSON gRPC dispatch, low-noise Go concurrency bug guards, and no checked-in build artifacts.
5. Performance reviews must now classify scan-heavy work into the correct lane:
   row-store transactional, compact read model/materialized view, columnar
   export/analytics, or runtime arena batch. The check is architectural rather
   than a shell gate because the right answer depends on query shape and product
   consistency semantics.

Go toolchain modernization is part of the lint baseline. `go_fix_check.sh` runs
`go fix -diff ./...` against each project Go module using a repo-local
`GOCACHE`; any suggested rewrite fails `lint-foundation` until it is applied and
covered. Schema-affecting suggestions such as `json:",omitzero"` require tests
that prove the before/after JSON contract intentionally changed.

## Communication Lane Enforcement

Foundation-generated apps inherit a boundary policy:

1. same-process or app-internal Go communication uses `grpcsvc.NewDirectFrameClient`, `Router.DispatchFrame`, `RegisterFrame`, or typed protobuf handlers
2. Go service-to-service communication uses binary `DispatchFrame` or typed protobuf contracts
3. JSON/map dispatch is ingress, admin, debug, or explicit compatibility behavior
4. HTTP and WebSocket ingress may accept JSON because they are external boundaries, but internal forwarding should preserve protobuf bytes or binary envelopes when negotiated
5. browser/runtime dispatch keeps the performance ladder order: `sab`, `wasm`, `transferable`, `ws`, `http`, `postMessage`

The generated checks fail app-internal `grpcsvc.Dispatch(...)` and `grpcsvc.Envelope` usage outside vendored foundation code and tests. If a project needs a compatibility adapter, keep it in a clearly named external boundary package and document the reason before adding an allowlist.

The generated checks also fail the Go concurrency patterns extracted from `docs/go_concurrency_bug_practices.md` when they are precise enough for a shell gate: zero-duration timer placeholders, select-default channel close guards, and likely `WaitGroup.Add` calls inside launched goroutines.

Broader risks such as lock/channel interleavings, select default behavior, timer/ticker ownership, anonymous goroutine closure inputs, and channel close ownership are surfaced by `go_concurrency_practices_check.sh` as `[REVIEW]` output. This script is copied into generated projects and called by `lint-foundation`. It is report-only by default and fails when `GO_CONCURRENCY_STRICT=1` is set.

The reason this is not all custom linter code:

1. stock linters are faster to maintain and track language evolution
2. Go custom analyzers and ESLint custom plugins are useful only when AST precision is needed beyond built-in rules
3. shell checks remain acceptable for repo-structure and forbidden-boundary checks because they are transparent, cheap, and easy to scaffold into apps
4. frontend rules intentionally avoid foundation-runtime strictness because React UI code often needs local composition, adapters, and gradual migration paths

## Columnar, VM, and ABI review baseline

Columnar storage, virtual-memory-aware arenas, and FFI calling conventions are
review obligations for performance-sensitive work.

Required review questions:

1. Does the workload mutate authoritative state, or is it append/read/report
   oriented? Mutating truth stays in Postgres row storage.
2. Does the query read many rows but few columns? Consider compact read models,
   materialized views, or columnar export before scanning transactional tables.
3. Does the runtime batch benefit from contiguous typed buffers? Prefer
   arena/column descriptors over row-object or JSON materialization.
4. Does the optimization depend on mmap/shared memory/large slabs? Include
   cold/warm page-cache evidence, page-fault counts where available, RSS/PSS,
   and descriptor reuse behavior.
5. Does the code cross FFI? Require ABI version checks, C-compatible exported
   signatures, pointer/length validation, UTF-8-safe diagnostics, and parity
   against a non-FFI lane.

## Delivery and Operational Enforcement

Generated projects inherit a lightweight delivery metrics collector rather than
a central dashboard. CI runs `scripts/checks/ci_delivery_metrics.mjs`, uploads
the JSON event artifact, and leaves aggregation to the app deployment platform.
The collector carries DORA, SPACE/DevEx, OpenTelemetry linkage, and supply-chain
fields for SBOM/provenance evidence.

Project scaffold checks verify:

1. Go 1.26 CI baseline
2. delivery-metrics artifact capture
3. operations runbook templates
4. configured CORS origins instead of wildcard scaffold defaults
5. protected operational endpoints for production posture
6. SBOM generation through the security workflow

`operational_excellence_check.sh` verifies those delivery and supply-chain
hooks without requiring a production deployment platform.

## Runtime and Formal-Methods Enforcement

Low-level runtime work has two separate gates:

1. `runtime_performance_contract_check.sh` verifies that performance docs and
   scripts expose pprof/trace, block/mutex profiles, CPU counter capture,
   machine metadata, Rust Miri/Loom opt-ins, WebGPU/WGSL, and CUDA/Nsight
   evidence hooks.
2. `formal_methods_check.sh` verifies that TLA/PlusCal/Alloy/P guidance and
   the queue, projection, and WebSocket starter modules are present in both
   Foundation and generated-project layouts.

The heavy tools remain opt-in. The default lint verifies the contract and the
reproducible entry points; profile, counter, Miri, Loom, and TLC execution are
selected by the engineer changing that lane.

## Agent Contract Enforcement

Agent-facing documentation is part of the scaffold contract because generated
applications are expected to be maintained by one architect and multiple coding
agents. The check enforces:

1. `agent_operating_contract.md` and `future_practices_research.md` are present
   in copied Foundation docs
2. `AGENTS.md`, `CLAUDE.md`, and the generated project README point agents to
   the operating contract
3. Makefiles expose `check-agent-contract` and include it in
   `lint-foundation`
4. scaffolded guidance references evidence requirements before domain or
   pre-production work
5. the machine-readable controls matrix remains present and checkable from
   both Foundation source and generated-project layouts

This is intentionally a documentation and workflow gate, not a substitute for
tests or static analysis. The evidence itself still belongs in the relevant
test, benchmark, migration, or review artifact.

## Coverage and hotspot baseline

The foundation baseline treats change risk as complexity plus coverage together, not either in isolation.

Recommended app-level thresholds:

1. new and changed production code line coverage >= 95%
2. new and changed production code branch coverage >= 90%
3. legacy code should improve toward 95% line / 90% branch when touched
4. CRAP-style hotspot scores above 30 should trigger tests or refactoring before merge

Recommended reporting posture:

1. publish machine-readable coverage output in CI
2. publish a human-readable hotspot summary for changed modules where tooling supports it
3. exclude tests, benchmarks, migrations, generated code, and other non-production artifacts from hotspot analysis

Stack mapping guidance:

1. Go: pair `go test` coverage with cyclomatic-complexity reporting in CI
2. TypeScript: pair Vitest/Jest coverage with ESLint complexity and hotspot review in PRs
3. .NET app consumers of this foundation should use OpenCover-compatible coverage plus ReportGenerator risk hotspots when they need CRAP-score reporting

## DOM observer baseline

The shared ESLint baseline restricts direct `MutationObserver` construction.

Reason:

1. the foundation architecture prefers explicit data flow through props, stores, route contracts, and runtime events
2. DOM mutation watching is easy to over-broaden, can cause feedback loops, and is a poor fit for auth, routing, or business state
3. `ResizeObserver` and `IntersectionObserver` are usually better matches for layout and visibility concerns

When an exception is justified:

1. keep it inside a narrow UI adapter or third-party integration wrapper
2. observe the smallest possible subtree with narrow options
3. disconnect reliably on cleanup
4. prove the behavior with tests or fixture-driven verification
