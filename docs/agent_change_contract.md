# Agent Change Contract

Status: 0.0.1
Owner: Platform Architecture

## Purpose

Foundation agents use one machine-readable pre-edit model instead of inventing
parallel planning, evidence, and risk formats. The model connects existing
systems:

- `tooling/agent_architecture_graph.json` locates capability owners,
  entrypoints, invariants, consumers, and required evidence.
- `docs/references/lifecycle/lifecycle_contract.json` remains authoritative for
  command lifecycle semantics.
- `tooling/practice_controls.psv` remains authoritative for enforceable rules.
- `docs/specs/tla/conformance.tsv` remains authoritative for formal invariant mappings.
- Existing protobuf, frontend prototype, lifecycle, and runtime generators remain authoritative for generated source.

The change model is architectural simulation before edits. It records command
terminals, projection, bounded worker posture, offline/realtime behavior,
conflict policy, fallback, invariants, evidence, and approval level.

## Commands

```bash
ovasabi agent graph --capability=live_projection
ovasabi add feature review-task --commands=create,assign,complete --projection=list --offline --realtime
ovasabi agent check --file=.foundation/changes/review-task.json
ovasabi agent evidence --plan=.foundation/changes/review-task.json
```

`add feature` writes `.foundation/changes/<feature>.json`. It does not generate
domain meaning or overwrite project code. After approval, agents use the
existing contract and scaffold generators to implement the declared slice.

## Risk-aware autonomy

| Risk | Scope | Approval | Minimum evidence |
| --- | --- | --- | --- |
| `tier1` | auth/policy, money/ledger, tenant/schema, crypto, WASM memory | required | unit, integration, contract, security review, human approval |
| `tier2` | services, workers, handlers, transport, projections | review | unit, contract, coverage |
| `tier3` | styling, copy, theme, layout, content | autonomous | lint, visual review |

Classification is conservative and visible. A reviewer may always raise the
tier; tooling must never silently lower it.

## Semantic checks

The model check rejects commands without requested/success/failed terminals,
unbounded workers, non-positive attempts, realtime plans without projections,
missing fallback/conflict policy, and Tier 1 plans without human approval.

Runtime proof remains owned by lifecycle contract tests, tenant-negative tests,
idempotency tests, optimized/fallback parity, generated projection checks, and
shutdown/leak tests. Naming an invariant is not proof.

## Evidence and ownership

The evidence command scaffolds objective, risk, files, contracts, invariants,
commands, coverage before/after, benchmarks, fallback, and known gaps. Agents
fill it during implementation; empty evidence remains visibly incomplete.

The graph, schema, and script are Foundation-owned. Models and ledgers under
`.foundation/changes/` are project-owned review artifacts. If orchestration is
unavailable, the same JSON may be authored manually and checked later.
