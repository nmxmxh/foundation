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

## Tooling

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
