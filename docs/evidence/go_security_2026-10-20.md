# Go Security Remediation: 2026-10-20

## Scope and result

Foundation now requires Go 1.26.10.
All twelve registered applications require the same patched minimum.
Their macOS and Linux scans report zero reachable vulnerabilities in application commands and internal packages.
These results describe local source and dependency graphs.
They do not establish deployed protection or absence of prior compromise.

## Advisory and ownership

[CVE-2026-111943](https://github.com/golang/go/issues/82345) concerns trust in selected intermediate certificates.
The [Go release history](https://go.dev/doc/devel/release#go1.26.10) identifies Go 1.26.10 and 1.27.3 as patched releases.
The release history dates these releases October 19; the advisory discussion includes October 20 activity.
The downloaded Go 1.26.10 source contains the intermediate-key rejection code.
Its complete `crypto/x509` test suite passed locally.

The scaffold manifest assigns application module files, Dockerfiles, and Compose files to create mode.
Foundation controls CI templates and the shared checks.
This change modifies the owning Core sources.
The existing managed patcher applies only `go_security` across the fleet.
It preserves unrelated application configuration and records each changed file in the existing patch ledger.
No complete fleet synchronization or manual change to vendored Core code occurred.

## Changes

1. Require Go 1.26.10 in all six Core modules and the backend template.
2. Pin Core CI, scaffold CI, Docker defaults, Compose defaults, and environment examples to the patched release.
3. Compare full patch versions in the consistency guard across 46 declarations.
4. Add the selected `go_security` patch for module requirements, workspaces, and effective build defaults.
5. Derive generated workspace versions from the application module to prevent repeated update drift.
6. Add regression tests for patch adoption, idempotence, preserved newer requirements, and stale version rejection.
7. Upgrade Core compression from `github.com/klauspost/compress` v1.18.6 to v1.18.7.
8. Update affected application dependencies through bounded `go get` commands.

The public API and event contracts did not change.
The operational contract now rejects toolchains older than Go 1.26.10.
Certificate verification remains enabled.
Automatic toolchain selection produced Go 1.26.10 inside Core.
An explicit local Go 1.26.6 invocation failed because it did not satisfy the module requirement.
If a patched toolchain is unavailable, the build must fail.
There is no fallback to an older, vulnerable toolchain.

## Additional dependency findings

Initial application scans found reachable advisories in older shared dependencies.
Core already used patched HTTP/2, OpenTelemetry, and gRPC releases.
Several applications had not adopted those versions.

| Dependency | Adopted version | Evidence |
| --- | --- | --- |
| `golang.org/x/net` | v0.60.0 | [HTTP/2 advisory](https://pkg.go.dev/vuln/GO-2026-6617) |
| `google.golang.org/grpc` | v1.83.2 | [gRPC advisory](https://pkg.go.dev/vuln/GO-2026-6348) |
| OpenTelemetry SDK and OTLP exporters | v1.45.0 | [OTLP advisory](https://pkg.go.dev/vuln/GO-2026-6505) |
| `github.com/klauspost/compress` | v1.18.7 | [Compression advisory](https://pkg.go.dev/vuln/GO-2026-5841) |
| `golang.org/x/image`, where required | v0.45.0 | [Image advisory](https://pkg.go.dev/vuln/GO-2026-6222) |

## Fleet scan results

The initial column records advisories after the Go toolchain update but before dependency remediation.
The scan target was `./cmd/... ./internal/...`.
Final Linux scans used `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`.
All scans used Go 1.26.10 and `govulncheck` against the live vulnerability database.
Each scan had an explicit deadline.

| Registered application | Initial reachable advisories | Final macOS | Final Linux |
| --- | ---: | ---: | ---: |
| chowdash | 14 | 0 | 0 |
| civic | 6 | 0 | 0 |
| docuos | 6 | 0 | 0 |
| forest | 6 | 0 | 0 |
| global | 6 | 0 | 0 |
| marketer | 6 | 0 | 0 |
| ovasabi | 0 | 0 | 0 |
| pronto | 11 | 0 | 0 |
| reframe | 6 | 0 | 0 |
| trader | 6 | 0 | 0 |
| trotters | 6 | 0 | 0 |
| metered | 6 | 0 | 0 |

The initial broad `./...` scan failed package loading in eleven applications.
Existing standalone tooling and copied module caches caused those failures.
Those failures remain recorded separately; they are not successful scans.
The final scope covers application commands, internal packages, and their transitive imports.
It excludes unrelated root tooling, separate modules, and dormant packages outside those imports.

Module-level advisory records can remain when affected packages or symbols are not imported or called.
Core has three such records: unmaintained OpenPGP and two legacy AWS S3 encryption advisories.
The final Core scan found no affected imported packages or reachable symbols for those records.

## Validation

- Full Go tests passed for all six Core modules under Go 1.26.10.
- `go vet ./...` passed across the six Core modules.
- `govulncheck ./...` passed across those modules.
- Compression, snapshot, and object-store tests passed after the compression update.
- Chowdash media tests passed after the image dependency update.
- The complete Go `crypto/x509` tests passed.
- A rebuilt Foundation CLI reports Go 1.26.10 through `go version -m`.
- Three security update regression tests passed.
- Scanner exit propagation and offline failure tests passed.
- Scaffold manifest, updater regression, and fresh scaffold idempotence checks passed.
- The Core validation contract passed.
- All twelve applications passed compilation checks.
- Six checks compiled all commands and internal packages without running their tests.
- The other six compiled server, worker, configuration, and startup tests without running them.
- All twelve selected application updates passed a second idempotence check.
- The user approved the protected checksum refresh; enforcement integrity then passed.

Detailed machine results and logs reside in `test-results/go-security-2026-10-20/`.
The fleet records include initial loading failures, dependency changes, final scans, and compilation results.
The manifest mapping did not change because source paths, destinations, and ownership modes remain the same.

## Remaining deployment work

WARNING: Existing executables retain the standard library used during compilation.
Rebuild and redeploy servers, workers, commands, and containers before claiming protection in production.
Inspect each deployed executable with `go version -m` after replacement.
Verify critical outbound TLS connections from the rebuilt application.
A rejected intermediate can cause legitimate integrations to fail until their certificate chain changes.
Do not bypass certificate verification to restore connectivity.

No production deployment, container replacement, database mutation, or remote incident investigation occurred.
Local tests do not establish endpoint compatibility, runtime protection, or historical compromise.
