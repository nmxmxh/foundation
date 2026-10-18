# Scaffold Manifest

Status: current as of 2026-05-29
Owner: Platform Architecture

The scaffold manifest is the contract that tells Foundation which files are
managed, where they land in generated projects, and how aggressively updates
may touch them.

The source file is `templates/scaffold.manifest.tsv`.

## Columns

| Column | Meaning |
| --- | --- |
| `source` | Foundation template or source asset. |
| `destination` | Generated project destination. |
| `profiles` | `all`, `full`, `backend`, or `frontend`, comma-separated when needed. |
| `feature` | `always`, `docker`, `wasm`, or `native`. |
| `mode` | `overwrite`, `force`, or `create`. |

## Modes

| Mode | Use |
| --- | --- |
| `overwrite` | Managed scaffold files that should update when Foundation updates. |
| `force` | Managed files where the generated baseline must win over local drift. |
| `create` | Seed files only. Existing app-owned files are preserved. |

Use `create` when a generated project is expected to customize the file. Use
`overwrite` or `force` only when Foundation owns the file’s contract.

## Seed Ledger and Drift Warnings

Create-mode files are recorded in a project-root seed ledger,
`.foundation-seeds.tsv` (`destination`, `template_sha256`, `seeded_sha256`).
Rows are written when a file is seeded; projects scaffolded before the ledger
existed are backfilled on their next update, treating current state as the
baseline.

During `update-project.sh` (and `ovasabi refresh`), every create-mode file is
compared against its ledger row:

- **Template unchanged** → silence. User edits to project-owned files are
  never flagged; customization is the point of `create` mode.
- **Template evolved, local copy unmodified since seeding** → warning with a
  reseed hint (delete the file and re-run update to reseed it).
- **Template evolved, local copy customized** → warning with a review hint
  (diff the local file against the current template).

Warnings repeat on every update until resolved: either reseed the file, or
run `update --acknowledge-seed-drift` / `refresh --acknowledge-seed-drift`
after review to re-baseline the ledger to the current templates. Drift
detection never rewrites a create-mode file.

The ledger is project state and should be committed. The seed-drift contract
is enforced by `make check-scaffold-seed-drift`
(`tests/scaffold_seed_drift_test.sh`).

## Declining a Seed: `.foundation-seeds.ignore`

`create` mode writes its template whenever the destination is **absent**, so
deleting a seeded file is indistinguishable from never having been seeded, and
the next update writes the file back. That is correct for a project that has
not been scaffolded yet and wrong for a project that removed the file on
purpose.

`.foundation-seeds.ignore` at the project root is the tombstone list: one
project-relative destination per line, `#` comments and blank lines ignored.

```text
# Destinations this project deliberately removed.
frontend/src/components/ui/Button.tsx
frontend/src/components/ui/Input.tsx
frontend/src/components/ui/index.ts
```

A tombstoned destination is never seeded, never drift-checked, and is dropped
from the seed ledger — Foundation stops tracking it entirely. Remove the line
to let the seed return on the next update.

Tombstones apply to `create` mode only. `overwrite` and `force` files carry a
Foundation-owned contract; a project that needs one of those gone should change
the manifest, not silence it locally.

Every update reports what it seeded: new seeds as a count, and any destination
that was seeded, later removed, and has now been written back as a per-file
warning naming `.foundation-seeds.ignore`. A resurrection is the one create-mode
write no project asks for, so it is never silent.

## Tooling

Synchronize project files without dependency resolution:

```bash
scripts/update-project.sh /path/to/project --skip-deps
# The CLI accepts the same option.
ovasabi update --project-dir /path/to/project --skip-deps
ovasabi refresh --project-dir /path/to/project --skip-deps
```

This option skips npm lockfile resolution and Go module tidy.
Run dependency synchronization later before building the updated application.
The updater reports changed manifests that still require synchronization.

Without this option, each updater dependency command has a 120-second deadline.
Set `FOUNDATION_DEPENDENCY_TIMEOUT_SEC` to an integer from 1 through 86400 to change that deadline.
The shared deadline helper requires Perl and terminates the command group on timeout.
An npm failure stops the update. A Go tidy failure preserves the existing warning and manual recovery path.
Source synchronization can already have completed when a dependency command fails.

List files by profile:

```bash
tooling/scripts/manifest_tool.sh list --profile frontend
```

List seed-only backend files:

```bash
tooling/scripts/manifest_tool.sh list --profile backend --mode create
```

Validate the manifest:

```bash
tooling/scripts/manifest_tool.sh validate
```

The manifest validator rejects malformed rows, missing sources, invalid modes,
invalid feature flags, and Foundation-only assets that must never be copied into
generated applications.

## Maintenance Rules

1. Prefer Foundation-owned shared modules over copying project-local helpers.
2. Do not add benchmark harnesses, service-backed test assets, or Foundation-only
   experimental packages to generated projects.
3. Use `create` for app-owned domain code, frontend product surfaces, and any
   file likely to carry local business logic.
4. Run `tooling/scripts/manifest_tool.sh validate` after edits.
5. Treat mode changes as architectural changes. Moving a file from `create` to
   `overwrite` or `force` can overwrite app-owned behavior during updates.
6. Agent config bundle files (`AGENTS.md`, `.cursorrules`, `.clauderules`,
   `CLAUDE.md`, and `.agents/*`) should remain `create` unless Platform
   Architecture explicitly accepts the risk of overwriting project-local agent
   guidance.

## Public edge recipes

The two `config/public-cache.*.example` files are optional create-mode seeds.
The scaffold never activates them automatically.
Application owners select public routes and freshness before activation.

1. Copy `public-cache.http.conf.example` into `/etc/nginx/conf.d/public-cache.conf` in the application image.
2. Enable only reviewed exact paths in its map. The default zone remains `off`.
3. Copy `public-cache.server.inc.example` into the same directory without its `.example` suffix.
4. Include that file inside the application server block.
5. Adjust its exact machine routes to match the application backend.
6. Run `nginx -t` and the application route tests before deployment.

The recipe caches anonymous GET and HEAD responses only.
Credentials, cookies, queries, ranges, unsuccessful responses, and upstream privacy directives prevent cache publication.
The cache key includes effective scheme, public host, upstream group, and original request URI.
Upstream `Vary`, `Set-Cookie`, `Cache-Control`, and `X-Accel-Expires` remain effective.
Expired entries require a successful origin response. No stale-response fallback is enabled.
Public routes must precede static extension rules and SPA fallback.
Canonical URLs, localization, publication rules, and route allowlists remain application-owned.

The default `/api/` and `/v1/` routes select `foundation_backend` for connection reuse.
Docker DNS refresh remains enabled through the shared upstream zone and `resolve` parameter.
Upstream Host values, upgrade headers, compression guards, and streaming behavior remain compatible.
The updater preserves custom pool sizes and custom log formats.
The standard log format now escapes JSON and records request identity, upstream timing, and cache outcomes.
`NONE` and missing instrumentation must not be reported as cache hits.
A 499 response records client closure. It does not identify the underlying bottleneck.

`make test-nginx-edge` exercises real Nginx against an instrumented origin.
CI requires Docker for this lane. Local runs report an explicit skip when Docker is unavailable.
The test removes its temporary containers and network.
It checks connection reuse, upgrades, Host values, route precedence, default caching, privacy, expiry, and cold coalescing.
