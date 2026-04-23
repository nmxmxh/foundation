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
