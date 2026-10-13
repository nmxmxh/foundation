# Foundation Review: October 13, 2026

## Scope and ownership

This review covers recent Core commits and the existing uncommitted database changes.
The initial checkout contained twelve modified files and four untracked files.
Existing work remains in place.
No production systems or sibling applications were changed.

The scaffold manifest assigns `create` ownership to configuration and startup templates.
It assigns `force` ownership to configuration tests.
These edits occurred in Core, which owns the template sources.
The diagnostic scripts and this report have no scaffold manifest row.

## Recent changes and lessons

| Change | Evidence in the repository | Lesson |
| --- | --- | --- |
| September stream improvements and Go WASM retirement | `docs/foundation_benchmarks.md`, `tests/retire_go_wasm.test.mjs` | Bound retained buffers and verify consumer artifacts during retirement. |
| October 2 Hermes changes, `8b4758c` | Scope bounds, bitmap saturation tests, escaped keys, and candidate validation | Bound auxiliary indexes and replay fences, not only record storage. |
| October 6 shared arena changes, `da1f19b` | Atomic descriptor claims, attachment tests, release publication tests | Shared memory requires ownership, lifetime, and publication rules. |
| October 6 WhatsApp changes, `da1f19b` | Configured account mappings, negative ingress tests, durable inbox interface | Acknowledge only after durable acceptance. Applications own inbox transactions and business processing. |
| Uncommitted retention and diagnostic work | Retention tests, configuration, SQL templates, and resource profiler | Diagnostic output and configuration precedence require failure tests. |

The October 6 commit title describes WhatsApp but also includes shared arena changes.
Review actual file changes when selecting validation scope.

The historical stream benchmark records 14.50 ms versus 0.90 ms for its 4 MiB fixture.
This review did not repeat that benchmark or establish current application latency.
Local test success does not establish provider, device, deployment, or fleet behavior.

## Improvements completed

### Diagnostic reliability

The original parser treated psql command status text as a table header.
That could discard result rows without reporting failure.
The client now suppresses status text and parses CSV with Python's CSV parser.
Malformed row widths fail explicitly.
Quoted names can contain commas, tabs, and newlines.

SSH commands previously interpolated container, user, database, and SQL text into shell strings.
The client now quotes remote argument arrays and sends SQL through standard input.
SSH uses batch mode, and psql disables password prompts and startup files.
SQL errors stop execution.
Client failures omit raw database output, which can contain private connection details.

Each query now uses an explicit read-only transaction.
Statements have a five-second timeout, and lock waits have a one-second timeout.
Client processes have a fifteen-second deadline.
Limits outside 1–1000 fail before a connection attempt.
Missing cache measurements remain unknown instead of reporting 100 percent.
Reports expose the database statistics reset time.

The two script copies remain identical, with a regression test for parity.
No live database profiling was possible because Docker was stopped and psql was absent.

### Retention precedence

The completed-job override originally defaulted to two hours.
That value overwrote the human and standard tier durations in the worker template.
The server template also ignored the configured tier and override.

An absent override now has a zero value.
Both template clients apply the selected tier and then a positive completed-job override.
Canceled and discarded retention remain controlled by the tier.
Unknown tiers retain the existing high-frequency fallback.
A configuration regression test covers absent and explicit overrides.

### Documentation accuracy

The database guide now distinguishes read-only diagnostics from zero-cost diagnostics.
It no longer claims that the profiler already exists in every generated project.
Cache estimates require workload evidence instead of an unconditional three-to-one ratio.
The PostgreSQL query-planning documentation supports that distinction.

## Validation

| Command | Result |
| --- | --- |
| `go test ./worker ./adapter/whatsappx ./hermes -count=1` in server-kit/go | Passed |
| `go test -race ./worker ./adapter/whatsappx ./hermes -count=1` | Passed |
| `go vet ./worker ./adapter/whatsappx ./hermes` | Passed |
| Focused browser tests for arena, surfaces, clients, workers, and marks | 119 tests passed across five files |
| `npm run typecheck` in browser-host | Passed |
| `python3 tests/deep_resource_profiler_test.py` | Ten tests passed |
| `GO111MODULE=off go test ./templates/backend/internal/config -count=1` | Passed |
| `bash tests/scaffold_manifest_test.sh` | Passed |
| `bash tests/scaffold_seed_drift_test.sh` | Passed |
| `git diff --check` | Passed |
| Enforcement integrity | Passed after the user approved the protected checksum refresh |

Profiler tests simulate client responses and argument construction.
They do not establish actual PostgreSQL behavior or remote execution.
No full Foundation suite, benchmark ratchet, coverage ratchet, or live retention test was executed.

## Remaining gaps

1. Existing databases still retain indexes removed from initialization SQL.
   Inspect actual query plans before writing a separate operational migration.
2. The legacy managed patch inserts a fixed high-frequency policy.
   Customized project composition roots need an explicit configuration migration.
3. The GIN gate uses a single-line pattern that can miss schema-qualified or multiline definitions.
4. Job counts, hit ratios, and zero-scan indexes require an observation window and workload context.
   A count alone cannot prove a missing retention policy or unnecessary index.
5. The agent contract still specifies unconditional cache and queue thresholds.
   Reconcile those thresholds with measured budgets before adopting them as fleet gates.
6. The profiler regression command remains standalone and needs CI registration during a tooling update.

## Definition of done

- Contract: Diagnostic output uses CSV internally and adds reset-time evidence.
  Template retention configuration now preserves tier selection.
  No event schema or application API changed.
- Invariants: Preserve bounded work, literal argument boundaries, and read-only diagnostic transactions.
- Evidence: Retain the commands and results above, plus regression tests.
- Fallback: Missing measurements remain unknown, and client errors terminate the audit.
  River retains its existing fallback for unknown tiers.
- Boundary: Changes affect Core tooling, Core documentation, and template sources.
- Regression guards: Cover parsing, remote quoting, deadlines, errors, limits, copy parity, and optional overrides.
- Documentation: Update `docs/database_practices.md` and retain this review as the evidence ledger.

## Primary references

- [PostgreSQL psql documentation](https://www.postgresql.org/docs/current/app-psql.html)
- [PostgreSQL query planning](https://www.postgresql.org/docs/current/runtime-config-query.html)
