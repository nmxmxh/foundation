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
