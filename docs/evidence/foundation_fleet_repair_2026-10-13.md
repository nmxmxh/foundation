# Foundation Fleet Repair: October 13, 2026

## Result

All twelve projects pass scaffold validation, startup and worker compilation, and River policy checks.
The changed packages in five projects also pass unit tests with race detection.
Their server and docgen entrypoints compile.
Detailed results reside in `test-results/fleet-repair-20261013/checks.json` and the adjacent logs.

No production deployment or database migration occurred.
These results establish local source validation only.

## Repairs

### Application composition

Civic and Global had empty service containers while their typed handlers expected populated domain fields.
Their previous constructors were recovered from project history and adapted to current Foundation interfaces.
Civic retains its audit bridge and domain handlers.
Global retains required-store validation, explicit test fallbacks, and typed wallet dispatch.
Global metadata now uses the canonical transport type.

Marketer now constructs its existing domain services and aggregates their typed registrations.
Trader now constructs its existing market service and exposes its typed route catalog.
Trader rejects a missing database instead of creating a broken service.
Legacy dispatch maps remain empty where typed registration owns the same event names.
This prevents duplicate registration.

Instance-free route catalogs retain their domain routes.
Tests validate their bindings and registration on both dispatch planes.

### Startup and worker wiring

Civic, Global, and Trader now load configuration fields already referenced by their startup code.
Cache defaults remain disabled.
Global now shares a Foundation Redis client with its optional cache.
Redis initialization precedes cache construction, and database initialization failure closes the Redis client.
Its health probe performs one read and preserves context and errors.
Disabled cache wiring preserves the original store.

Civic's worker readiness gate now uses the actual logger variable.
Its two recurring jobs explicitly restrict uniqueness to non-terminal states.
The existing recurring-job regression guard passes.

### Metered

Metered's amount utility moved from `internal/service/ledger` to `internal/ledger` without arithmetic changes.
It is a domain primitive, not an implemented ledger service with its own protobuf API.

The existing financial protobuf RPCs now have typed registry and frame bindings.
They delegate to the generated `UnimplementedMeteredServiceServer` methods.
Tests require `codes.Unimplemented` and reject successful financial responses.
Startup installs the bindings represented by the HTTP route catalog.

NOTE: Payment evidence persistence and treasury execution remain unimplemented.
Passing structural checks does not establish financial readiness.

### Updater guards

Managed cache patches require compatible configuration and import aliases before changing startup references.
They do not introduce a `wrapped` reference before the wrapper exists.
Readiness insertion recognizes loggers that use `ErrorContext`.
Regression tests cover missing configuration, unsupported aliases, actual pool names, and repeated application.
Core updater regression tests and enforcement integrity pass.

## Ownership and contracts

The scaffold manifest assigns `create` ownership to the edited composition, configuration, and worker sources.
Additional handlers and regression tests are project-owned.
Foundation references were synchronized through the official updater, not manually edited.
Core owns the managed patch fixes and their regression tests.

No protobuf schema changed.
Application constructors and catalogs were restored.
Metered adds explicit unavailable bindings for previously declared RPCs.
No financial operation returns fabricated success or changes durable state.

## Validation

| Check | Result |
| --- | --- |
| Scaffold validation | 12/12 passed |
| Startup and worker compilation | 12/12 passed |
| River policy | 12/12 passed |
| Changed bootstrap, config, startup, worker, and worker-entry packages | Tests passed with race detection in five projects |
| Metered amount utility | Existing tests passed with race detection |
| Server and docgen entrypoints | Compilation passed in five projects |
| Managed startup guards | Three regression tests passed |
| Core updater regression suite | Passed |
| Protected tooling integrity | Passed |
| Whitespace checks for changed application files | Passed |

Unrelated baseline whitespace findings remain in Civic and Global vendored documentation and SQL source.
The changed application files have no whitespace findings.
Live database, Redis, provider, deployment, and financial operation behavior remain unverified.

## Definition of done

- Contract: Restore existing application composition and add explicit unavailable Metered bindings.
- Invariants: Preserve store requirements, metadata types, typed registration, bounded probes, and financial failure behavior.
- Evidence: Retain the package tests, race results, binding tests, fleet checks, and updater regression tests.
- Fallback: Disabled caches preserve their stores; unsupported financial RPCs return protocol errors.
- Boundary: Application services remain application-owned; shared patch mechanics remain in Core.
- Guards: Add composition tests, cache and probe tests, unavailable-operation tests, and managed patch tests.
- Documentation: Retain this handoff and record Metered's unavailable protocol state in its README.
