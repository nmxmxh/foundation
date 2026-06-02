# {{PROJECT_NAME}}

Foundation project generated from {{MODULE_PATH}}.

Read `AGENTS.md`, `.agents/DOMAIN_GUIDE.md`,
`docs/foundation/agent_operating_contract.md`,
`docs/foundation/practice_controls.md`, and
`docs/foundation/coding_practices.md` before editing architecture-sensitive
code.

Agent Definition of Done:

1. State whether a public contract changed.
2. Identify the invariant that must still hold.
3. Leave evidence through a test, benchmark, static check, review note, or migration proof.
4. Preserve or document the fallback path.
5. Name the scope boundary touched.
6. Add or update a regression guard.
7. Update docs or explain why no documentation changed.

## Commands

- `make dev`
- `make test`
- `make lint-foundation`
- `make foundation-update`

## Rules

1. Foundation owns structure and shared contracts.
2. Project code owns domain behavior.
3. Shared behavior should graduate into `foundation/server-kit` or another foundation module.
4. Run `make check-agent-contract` and `make check-practice-controls` after changing scaffold, docs, practices, or AI-agent workflows.
5. Run `make check-runtime-performance-contracts`, `make check-formal-methods`, and `make check-operational-excellence` after changing runtime lanes, formal specs, delivery telemetry, SBOM/provenance hooks, or operations workflows.
