# Migration Log

Status: project-owned

Use this log for Phase 2 production migrations after schema freeze. Foundation
active-development migrations can remain folded into the first three migration
groups until the Phase 2 ADR is accepted.

| Migration | Environment | Estimated runtime | Lock impact | Rollback | Backup verified |
| --- | --- | --- | --- | --- | --- |
| <!-- 0004_example_add_field --> | <!-- staging/prod --> | <!-- from staging run --> | <!-- low/medium/high --> | <!-- down migration or manual recovery --> | <!-- timestamp --> |

Required before running a Phase 2 migration:

- [ ] Phase 2 ADR is accepted under `docs/decisions/`
- [ ] Expand/contract sequence is documented
- [ ] Staging runtime and lock impact are recorded
- [ ] Rollback path is documented
- [ ] Production backup restore proof is current for the deployment window
