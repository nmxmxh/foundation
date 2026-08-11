# Scaffold Ownership Boundary

Status: baseline
Date: 2026-08-11
Owner: Platform Architecture

## Purpose

Foundation expects to be used for a decade. Over that time its Go, Rust, and
TypeScript layers will be replaced several times over, container base images
will move, and the shape of the scaffold will change. The projects generated
from it must survive all of that.

This document fixes the boundary that makes both possible at once: **Foundation
keeps moving forward, and a project's domain does not break.** It answers three
questions that were previously answered by convention and by whoever wrote the
last patch:

1. Which changes may Foundation push into an existing project on its own?
2. Where does a project record a decision Foundation must respect?
3. How does a fix reach a project scaffolded two years ago, and when does that
   fix get deleted?

## 1. The axis is reversibility, not language

The instinct is to split "Foundation code" from "project code". That is the
wrong cut, because it puts a Postgres major version and a Go minor version on
the same side of the line when they behave nothing alike.

The distinction that actually predicts what is safe is: **what does reverting
cost?**

| class | reverting costs | who decides | examples |
| --- | --- | --- | --- |
| **reversible** | changing a number back | Foundation | Go, Node, Alpine, migrate CLI, every Go/Rust/TS package |
| **irreversible** | restoring from backup | **the project** | Postgres major, Redis major, applied migrations, on-disk formats |
| **app-owned** | nothing, it was never Foundation's | the project | which database distribution, ports, resource limits |
| **domain** | it is the project's own code | the project | services, migrations, business logic |
| **contract** | breaks already-deployed consumers | additive-only | capnp schemas, config-contracts |

Go 1.25 → 1.26 writes no state a downgrade cannot read. Put 1.25 back and you
are exactly where you were. Foundation can move it for you.

Postgres 18 → 19 runs `pg_upgrade` and rewrites the data directory. Putting 18
back does not undo that. **No automated update may ever initiate it**, and this
is not a matter of taste — it is the one change that can destroy a project's
data by being helpful.

This is why the wrappers can stay fluid while the domain stays stable. Not
because Go is special, but because Go is reversible.

Declared in `tooling/versions.tsv`, enforced by `make check-version-consistency`.

## 2. Ranges, not equality

The naive check asserts a project's version equals Foundation's current one.
That guarantees every project is dragged forward forever, which is exactly what
must not happen to an irreversible version.

Foundation instead declares a **supported range**, and a project declares a
**point inside it**:

```
tooling/versions.tsv   POSTGRES_VERSION  18  irreversible  15..18
project .env           POSTGRES_VERSION=16                    ✓ supported
```

Staying on 16 is a supported state, not drift. A project scaffolded in 2027 keeps
passing its checks in 2035 without anyone having to remember it exists. Reversible
keys use `current` instead of a range, because there is no cost to staying current.

Raising a range's floor is a breaking change for projects below it and needs a
migration note in `CHANGELOG.md`.

**The check reads `.env`, not `.env.example`.** This matters more than it looks.
`.env` is what Docker Compose resolves at run time; `.env.example` is only the
seeded default and is patchable. Validating the example while the container runs
from the real file is how you end up with every gate green, the docs saying 19,
and the database on 18.

```bash
tooling/scripts/version_consistency_check.sh --app /path/to/project
```

## 3. Distribution is a parameter, not a fork

A project may need PostGIS, TimescaleDB, pgvector, or Valkey. Previously the base
image was hardcoded in `Dockerfile.postgres`, so wanting PostGIS meant forking
that file — and a forked file stops receiving every later fix to the config
wiring inside it. The project traded a database extension for a maintenance
dead end.

The distribution is now a build arg:

```bash
POSTGRES_BASE_IMAGE=postgis/postgis
POSTGRES_VERSION=16-3.4
```

Foundation has no opinion on the distribution (`app-owned` in the manifest). What
it still constrains is the **engine major**, because that is what decides whether
an existing data directory can be read. `16-3.4` range-checks as 16.

Note the naming: `*_BASE_IMAGE` is a repository without a tag, while the existing
`*_IMAGE` convention (`TEST_POSTGRES_IMAGE=postgis/postgis:17-3.5`) is a full
reference. Keeping those distinct matters — a project carrying the older
`REDIS_IMAGE=redis:8-alpine` would otherwise produce `redis:8-alpine:8-alpine`.

## 4. Where a project records a decision

Three places, in order of preference:

**`.env`** — for anything the compose file already parameterises. Versions,
distributions, credentials, ports. Never overwritten; it is created once by
copying `.env.example` and is the project's from that moment.

**`docker-compose.override.yml`** — for changing the *shape* of a service rather
than a value it already exposes. Compose merges it over `docker-compose.yml`
automatically, with no flags, and Foundation never writes to it. Use it for extra
services, resource limits, published ports, and bind mounts.

**`docker-compose.yml` itself** — only when the change should keep receiving
Foundation's maintenance. Managed patches reach into this file deliberately (see
below), so a value set here may be revisited.

The override file exists to remove ambiguity. A managed patch cannot always tell
a default nobody touched from a value chosen on purpose; anything in the override
is unambiguously the project's, and the merge guarantees it wins.

## 5. Managed patches, and why they cross the line

`tooling/scripts/scaffold_managed_patches.sh` edits project-owned files. **This is
intentional and it is the point of the mechanism.** A project scaffolded two years
ago cannot benefit from a fix to service wiring, a healthcheck, or a network alias
any other way. Files marked `create` in `scaffold.manifest.tsv` are the project's
to edit, but they are not frozen — managed patches are how remediation is
delivered to code that has already shipped.

They behave like database migrations: one-way, idempotent (each is guarded by a
search for the state it fixes), and applied in passing during an update.

What they lacked was a lifecycle. Three things now supply it.

### Provenance

Every patch declares `# @since <version>`. Without it, a patch can never be
retired, because nobody can prove every supported project has already received
it.

### A durable ledger

`log_patch` used to print and forget, so the only evidence a patch had ever run
was somebody's terminal scrollback. It now appends to `.foundation-patches.tsv`
in the project:

```
# applied_at	foundation_version	patch
2026-08-11T01:07:53Z	0.0.1	GO_VERSION retargeted to 1.26: Dockerfile
```

"Which fixes has this project received?" is now a question with an answer.

### Retirement

`tooling/min_supported_foundation` names the oldest version a project may be
updated *from*. A patch whose `@since` is strictly older than that has, by
definition, already been applied everywhere it could apply.
`make check-managed-patch-hygiene` reports those as retirable, and also fails on
a patch that is defined but never invoked — the quietest kind of stale, since it
reads like live behaviour during review.

Retiring them is the intended maintenance step. The file only shrinks if someone
acts on the report.

### The re-fire hazard

This is the concrete reason retirement matters rather than being tidiness. A
patch searching for `GO_VERSION=1.25` lies dormant for years and then fires the
day someone legitimately pins 1.25. A search string is live forever, and its
blast radius grows as the string becomes plausible again.

## 6. Version patching is manifest-driven

The old form hardcoded a migration pair per bump — "rewrite 1.25 to 1.26" — which
fails in two directions. A project on 1.24 matches no pair and is **silently
stranded**. And every historical pair must be kept forever or older projects stop
upgrading, giving an ever-growing ladder nobody can safely prune.

Patches now read the wanted value from `tooling/versions.tsv` and rewrite whatever
is there. State is O(1) per key rather than O(n) accumulated pairs, and no project
can be stranded by failing to match a literal. Adding Go 1.27 is a one-line
manifest edit.

The class column gates the rewrite: reversible keys are retargeted, irreversible
keys are reported and left alone.

## 7. The fail/warn policy

| condition | behaviour | why |
| --- | --- | --- |
| reversible version differs | **warn**, and retarget it | reverting costs nothing |
| app-owned value differs | silent | it was never Foundation's |
| irreversible version differs but is in range | **hold**, report, never touch | a supported state |
| irreversible version outside range | **fail**, halt the update | proceeding could run migrations against an unsupported engine |

The last row is the only condition that stops an update, and it is the one this
entire boundary exists to prevent.

## 8. Rules

1. Classify every new version key in `tooling/versions.tsv` before adding it
   anywhere else. The manifest is the source; every other site is a copy.
2. Never write an automated path that bumps an irreversible version in a project.
3. Irreversible keys get ranges. Reversible keys get `current`.
4. Validate projects against `.env`, never `.env.example`.
5. New managed patches declare `# @since`. Patches below the minimum supported
   version get deleted, not kept "just in case".
6. Prefer parameters over forks. If a project has to edit a Foundation file to
   express a choice, that choice should have been a variable.
7. A project's deliberate decisions belong in `.env` or
   `docker-compose.override.yml`, where the merge guarantees they win.

## References

1. Version manifest: `tooling/versions.tsv`
2. Minimum supported version: `tooling/min_supported_foundation`
3. Consistency gate: `tooling/scripts/version_consistency_check.sh`
4. Patch hygiene gate: `tooling/scripts/managed_patch_hygiene_check.sh`
5. File ownership and propagation: `tooling/foundation_ownership.tsv`
6. Seed ledger for create-mode files: `scripts/lib/scaffold.sh`
7. Scaffold modes: `templates/scaffold.manifest.tsv`, `docs/scaffold_manifest.md`
8. Docker Compose merge semantics:
   <https://docs.docker.com/compose/how-tos/multiple-compose-files/merge/>
