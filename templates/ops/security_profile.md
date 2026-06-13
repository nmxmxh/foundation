# Application Security Profile

Status: scaffold-baseline
Date: {{TIMESTAMP}}
Owner: Application team

This file is project-owned. Foundation creates it once so each generated
application has a local threat model on top of the shared Foundation security
floor in `docs/foundation/security_practices.md`.

## Application Identity

| Field | Value |
| --- | --- |
| Application name | {{PROJECT_NAME}} |
| Domain | To be completed by the application team before production |
| Data sensitivity | To be classified before production |
| Regulatory obligations | To be reviewed before production |
| Live users? | no |
| Last reviewed | {{TIMESTAMP}} |
| Reviewer | Scaffold baseline |

## Threat Surface Summary

List application-specific boundaries that are not fully described by the
Foundation baseline:

1. To be completed before production.

## Data Classification

| Data type | Sensitivity | Where stored | Who can access |
| --- | --- | --- | --- |
| To be completed before production | To be classified | To be documented | To be authorized |

## Auth and Access Control

1. Authentication methods: To be completed before production.
2. Multi-tenancy model: To be completed before production.
3. Role model: To be completed before production.
4. Privilege escalation paths: To be completed before production.
5. External identity providers: To be completed before production.

## Rate Limiting and Abuse Surface

| Endpoint / action | Rate limit | Burst | Who can abuse it |
| --- | --- | --- | --- |
| To be completed before production | To be set | To be set | To be analyzed |

## Third-Party Integrations

| Integration | Trust level | Data shared | Validation done |
| --- | --- | --- | --- |
| To be completed before production | To be classified | To be documented | To be tested |

## Incident Response

1. On-call owner: To be completed before production.
2. Escalation path: To be completed before production.
3. Disclosure policy: To be completed before production.
4. Last incident: none recorded in scaffold baseline.

## Review Checklist

- [ ] Application-specific entry points are documented.
- [ ] Data classes and retention obligations are classified.
- [ ] Auth model and object-level authorization risks are documented.
- [ ] Rate limits are implemented and tested for exposed flows.
- [ ] Third-party integrations validate signatures, schema, freshness, and size.
- [ ] Incident response contacts are current.
- [ ] Profile has been reviewed for the current application release.
