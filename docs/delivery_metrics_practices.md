# Ovasabi Delivery Metrics Practices

Status: baseline
Date: 2026-05-11
Owner: Platform Architecture

## Purpose

Runtime metrics explain whether the system is healthy. Delivery metrics explain whether the team can safely change it. Foundation projects should collect both.

This practice keeps delivery telemetry scaffold-friendly: generated projects inherit a small CI event collector and app-owned runbooks, while Foundation Core keeps aggregate analysis and service-backed benchmarks outside the scaffold.

## DORA Signals

Foundation projects should collect the five current delivery signals:

1. Change lead time: time from commit or PR creation to successful deploy signal.
2. Deployment frequency: successful production deploy events over time.
3. Failed deployment recovery time: time from incident or failed deploy start to recovery.
4. Change fail rate: production deploys that cause incident, rollback, hotfix, or failed health checks.
5. Deployment rework rate: reruns, rollbacks, follow-up deploys, or corrective changes caused by a prior deployment.

The scaffolded `scripts/checks/ci_delivery_metrics.mjs` emits one JSON event per CI run. Treat it as collection, not analysis. Teams can ship the artifact to a warehouse, object store, or observability backend when the deployment system is chosen.

## Incident Records

Each production incident should have a small structured record with:

1. Incident ID, severity, service, tenant impact, and user-visible symptom.
2. Start, detection, mitigation, and resolved timestamps.
3. Triggering change, deployment run, commit SHA, and rollback/hotfix links.
4. Root cause category and prevention follow-up.
5. Correlation IDs, trace links, dashboard links, and affected event contracts.

Do not store secrets, tokens, customer PII, or full payload dumps in incident records.

## Scaffold Boundary

Generated projects inherit:

1. `make delivery-metrics`
2. CI artifact capture for `delivery-metrics/ci-event.json`
3. `docs/operations/` templates for delivery metrics and incidents
4. Foundation docs under `docs/foundation/`

Generated projects do not inherit Foundation Core live benchmark daemons or aggregate DORA dashboards. Those belong either in the app's deployment platform or in a shared operations project.

## Review Checklist

- [ ] CI emits a delivery metrics event on every push and pull request.
- [ ] Deployment jobs emit explicit success/failure/rework signals.
- [ ] Incidents record start and resolved timestamps.
- [ ] Production changes can be tied back to commit SHA, run ID, and correlation IDs.
- [ ] DORA aggregation excludes local-only benchmark and scaffold-generation runs unless intentionally tracked.
