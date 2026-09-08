# Documentation Changelog

Status: active record
Owner: Platform Architecture

This file records structural changes to Foundation documentation. It answers
one question: where did the text that used to be here go?

Content changes inside a document do not belong here. Git history covers those.
Record a change here when a document is split, archived, renamed, merged, or
reduced, so a reader who remembers a section can still find it.

Newest entries first.

---

## 2026-09-08 — Benchmarks split into current results and archive

**Why.** `foundation_benchmarks.md` had grown to 4,632 lines, which was 21
percent of all Foundation documentation. It was an append-only log of 49
entries, and the entries were not in date order. Nothing in it separated a
current result from a superseded one, so a reader could not tell which numbers
still described the running system.

**What changed.**

| Document | Before | After |
| :--- | ---: | ---: |
| `foundation_benchmarks.md` | 4,632 lines | 232 lines |
| `info/benchmarks_archive.md` | new | 4,651 lines |

`foundation_benchmarks.md` now holds current results for the main lanes only,
written to `ste_documentation_practices.md`. It covers the Hermes hot plane, the
projection read path, the delete lane, the shared-memory transport, the epoch
exchange, the shared render surface, and HTTP ingress. It also keeps the method
sections: how to run, what the numbers mean, the statistical rules, and the
guardrails.

`info/benchmarks_archive.md` holds the whole previous document, unchanged. No
text was deleted. Superseded runs, rejected approaches, and the reasoning behind
each result are all there.

**Where things went.** Every section that is no longer in
`foundation_benchmarks.md` is in `info/benchmarks_archive.md` under its original
heading. Archived sections include the full baseline validation runs, the staged
local and service-backed load research, the historical reference runs from May
2026, the browser shared-arena reference, the runtime SDK comparison, and the
server-kit lane refresh series.

**What to read now.** For a current number, read `foundation_benchmarks.md`. For
why a lane has its present shape, read the archive.

## 2026-09-08 — Completed plans moved out of `hermes_hotplane.md`

**Why.** The document carried a "Benchmark Plan" and an "Implementation
Sequence". Both described work that was finished. The benchmark list named 18
benchmarks, and 11 of them never existed under those names, because the real
benchmark work took a different shape. A reader could not tell the plan from the
contract.

**What changed.** `hermes_hotplane.md` went from 665 to 606 lines. Both sections
were replaced by a pointer. The original text is in `info/superseded_plans.md`,
which is the new home for planning material that its own work has overtaken.

## 2026-09-08 — Projection read authorization documented

`projection_freshness_contract.md` gained three required fields for every
projection note: audience, authorization enforcement point, and field allowlist.
The first eight fields were all about freshness, so a projection note could not
state who may read the records it describes.

`security_practices.md` gained a projection-read entry in the vulnerability
table and an advisory dated 2026-09-08. The read path decides authorization by a
wire scope rather than by a service, and the document did not say so.
