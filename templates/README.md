# {{PROJECT_NAME}}

Built with Ovasabi Foundation {{FOUNDATION_VERSION}}.

## Quick Start

```bash
make setup
make docker-up
make dev
```

## Architecture

- Platform modules are vendored under `foundation/`.
- Managed scaffold is refreshed by `make foundation-update`.
- Project-owned behavior belongs under domain services, handlers, workers, and startup wiring.

## Agent-Native Workflow

Before changing architecture-sensitive code, read `AGENTS.md`,
`docs/foundation/agent_operating_contract.md`, and
`docs/foundation/practice_controls.md`. For new practices, performance lanes,
security posture, or AI-agent workflow changes, also read
`docs/foundation/future_practices_research.md`.

Architecture-sensitive changes must leave evidence through tests, benchmarks,
static checks, review notes, migration proof, or formal/spec evidence. Run
`make check-agent-contract`, `make check-practice-controls`,
`make check-runtime-performance-contracts`, `make check-formal-methods`, and
`make check-operational-excellence` after changing docs, scaffold, practices,
runtime lanes, formal specs, operations telemetry, or agent instructions.

Run `make lint-foundation` before committing foundation or scaffold changes.
