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

## SPACE and DevEx Signals

DORA explains delivery flow and reliability. SPACE keeps the system from
collapsing developer experience into one throughput number. Foundation events
should leave fields for:

1. satisfaction or local developer-experience survey score
2. performance regression or benchmark signal
3. activity count such as commit count for the event payload
4. communication/review latency
5. efficiency/flow signals: setup time, reruns, flaky tests, and rework

Do not use SPACE fields as individual performance scoring. Use them to detect
friction in the engineering system: slow setup, noisy tests, repeated reruns,
review queues, and mismatch between delivery speed and product safety.

## OpenTelemetry Linkage

Delivery and incident events should be joinable with runtime telemetry. Use
OpenTelemetry semantic conventions for service/resource names, trace IDs,
span IDs, deployment environment, and service version when exporting to an
observability backend. The CI collector keeps `otel_semconv_version`,
`trace_id`, `span_id`, `service_name`, and `correlation_id` fields so deployment
events can be linked to incidents, traces, and runtime dashboards.

## Supply Chain Evidence

Security workflow outputs and delivery events should preserve SLSA/SBOM context:

1. SBOM path and format, currently SPDX JSON in the scaffolded security workflow
2. SLSA provenance or attestation path when the deployment platform emits one
3. artifact digest and builder ID
4. source ref and source SHA

Treat provenance as operational evidence, not a badge. A deploy event without
artifact identity, source SHA, and SBOM/provenance hooks cannot support fast
incident triage or rollback confidence.

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
