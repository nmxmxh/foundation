# Enforcement

The tooling layer exists to turn blueprint practices into repeatable checks.

It does not replace app-specific CI, but it sets the baseline for:

1. coding practices
2. database practices
3. redis practices
4. migration structure
5. contract drift

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

Native tool mapping:

1. Go: `golangci-lint` owns unchecked errors, context use, body closing, security scans, static analysis, complexity, and allocation hints.
2. Rust: `cargo fmt` and `cargo clippy -D warnings` own formatting, unwrap/expect/panic discipline, and warning-free runtime code.
3. TypeScript/React: ESLint owns React hooks, import boundaries, observer exceptions, blocking atomics, and app-local raw WebSocket construction. TypeScript owns generated contract shape through `typecheck`.
4. CP scripts own foundation-specific communication and performance rules: no oversized runtime control buffer, no hot-path dynamic JSON envelopes in foundation runtime lanes, no compatibility gRPC envelope as a default, no app-internal JSON gRPC dispatch, and no checked-in build artifacts.

## Communication Lane Enforcement

Foundation-generated apps inherit a boundary policy:

1. same-process or app-internal Go communication uses `grpcsvc.NewDirectFrameClient`, `Router.DispatchFrame`, `RegisterFrame`, or typed protobuf handlers
2. Go service-to-service communication uses binary `DispatchFrame` or typed protobuf contracts
3. JSON/map dispatch is ingress, admin, debug, or explicit compatibility behavior
4. HTTP and WebSocket ingress may accept JSON because they are external boundaries, but internal forwarding should preserve protobuf bytes or binary envelopes when negotiated
5. browser/runtime dispatch keeps the performance ladder order: `sab`, `wasm`, `transferable`, `ws`, `http`, `postMessage`

The generated checks fail app-internal `grpcsvc.Dispatch(...)` and `grpcsvc.Envelope` usage outside vendored foundation code and tests. If a project needs a compatibility adapter, keep it in a clearly named external boundary package and document the reason before adding an allowlist.

The reason this is not all custom linter code:

1. stock linters are faster to maintain and track language evolution
2. Go custom analyzers and ESLint custom plugins are useful only when AST precision is needed beyond built-in rules
3. shell checks remain acceptable for repo-structure and forbidden-boundary checks because they are transparent, cheap, and easy to scaffold into apps
4. frontend rules intentionally avoid foundation-runtime strictness because React UI code often needs local composition, adapters, and gradual migration paths

## Coverage and hotspot baseline

The foundation baseline treats change risk as complexity plus coverage together, not either in isolation.

Recommended app-level thresholds:

1. new code line coverage >= 80%
2. new code branch coverage >= 60%
3. legacy code should improve toward 60% line / 40% branch at minimum when touched
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
