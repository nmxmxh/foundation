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

Run `make lint-foundation` before committing foundation or scaffold changes.
