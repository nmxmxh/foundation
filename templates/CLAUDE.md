# {{PROJECT_NAME}}

Foundation project generated from {{MODULE_PATH}}.

Read `AGENTS.md`, `.agents/DOMAIN_GUIDE.md`, and `docs/foundation/coding_practices.md` before editing architecture-sensitive code.

## Commands

- `make dev`
- `make test`
- `make lint-foundation`
- `make foundation-update`

## Rules

1. Foundation owns structure and shared contracts.
2. Project code owns domain behavior.
3. Shared behavior should graduate into `foundation/server-kit` or another foundation module.
