# Operations Bootstrap

This folder is project-owned. Foundation creates it once so each app can define production readiness without mixing app operations into Foundation Core.

## Delivery Metrics

CI emits a JSON event with DORA-ready fields through:

```bash
make delivery-metrics
```

Start by preserving the `delivery-metrics` CI artifact. When deployment is wired, forward those records to your metrics warehouse or observability backend and add deploy success/failure/rework signals from the deployment job.

Track:

1. Change lead time
2. Deployment frequency
3. Failed deployment recovery time
4. Change fail rate
5. Deployment rework rate

## Security Defaults

Production should keep:

1. `APP_ENV=production`
2. `REQUIRE_AUTH=true`
3. `PROTECT_OPERATIONAL_ENDPOINTS=true`
4. `ALLOWED_ORIGINS` set to exact origins only
5. `/metricsz`, `/metricsz/trace`, and operational event views behind authenticated operator/admin access

## Incident Records

Use `incident_record_template.md` for production-impacting failures, failed deployments, rollbacks, and security-relevant events.
