# Practice Controls Matrix

Status: mandatory
Owner: Platform Architecture

## Purpose

Foundation practices must be executable by one architect and many agents. The
source of truth is `tooling/practice_controls.psv`: a machine-readable matrix
that maps every `CP-*` and `TE-*` rule to its owning document, risk class,
automation strength, enforcement path, required evidence, and merge-gate
posture.

This document explains how to maintain the matrix without turning practice docs
into stale prose.

## Control Fields

| Field | Meaning |
| --- | --- |
| `control_id` | Stable rule or cross-cutting control ID. |
| `owner_doc` | Canonical document that owns the control. |
| `category` | Main lane: coding, testing, security, runtime, agent, tooling, etc. |
| `risk` | `low`, `medium`, `high`, or `critical`. |
| `automation` | `strong`, `partial`, `contextual`, or `human`. |
| `enforcement` | Semicolon-separated checks: `script:...`, `review`, or both. |
| `evidence` | Artifact required from an agent or reviewer. |
| `merge_gate` | `yes`, `no`, or `contextual`. |

## Automation Classes

`strong` means the rule is low-noise and expected to fail CI when violated.
`partial` means scripts catch common violations but review still owns judgment.
`contextual` means the right evidence depends on the lane, workload, or product
state. `human` means enforcement is reviewer-owned until a low-noise check
exists.

Do not mark a rule `strong` because it is important. Mark it `strong` only when
the automation has enough precision to avoid pushing agents toward worse code.

## Agent Workflow

When an agent changes a practice, check, scaffold default, or generated-project
contract:

1. Update the owning document.
2. Update `tooling/practice_controls.psv` if the rule ID, evidence, or
   enforcement changed.
3. Run `make check-practice-controls`.
4. If a new script or protected control file was added, refresh the enforcement
   manifest under the supervised update gate.

## Review Rules

1. Every `CP-*` and `TE-*` heading must appear in the matrix.
2. Every matrix script reference must exist in Foundation source and remain
   valid after scaffold sync.
3. Human-only controls must still name concrete evidence.
4. Cross-cutting controls use stable prefixes:
   `AOC-*`, `EVID-*`, `FPR-*`, `AISEC-*`, `PERFLAB-*`, `RUNTIME-*`, `FORMAL-*`,
   `OPS-*`, `PROJFRESH-*`, `MATH-*`, and `CTRL-*`.
   - `MATH-*` owns numerical-analysis, probability, statistics, floating-point,
     and algebraic-convergence rules in `mathematical_practices.md`. `MATH-01`
     requires the `mathematical-practices-checklist` evidence on changes to
     financial arithmetic, probabilistic structures, statistical metrics,
     floating-point reductions, or CRDT-shaped merges.
