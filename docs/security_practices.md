# Ovasabi Security Practices

Status: v1.0
Date: 2026-05-08
Owner: Platform Architecture

## Purpose

This document converts recurring real-world web vulnerability patterns into Foundation engineering controls. It is a defensive synthesis for code review, scaffold generation, and regression testing; it intentionally avoids storing exploit payload catalogs in the repository.

Primary references for current practice:

- OWASP Top 10 2025 draft: broken access control, misconfiguration, supply chain, cryptography, injection, insecure design, authentication, integrity, logging/alerting, and exceptional-condition handling.
- OWASP API Security Top 10 2023: BOLA, broken auth, object-property authorization, resource consumption, function authorization, sensitive business flows, SSRF, misconfiguration, inventory, and unsafe third-party API consumption.
- OWASP Cheat Sheet Series: SSRF, CSRF, file upload, XXE, logging, mass assignment, and OAuth guidance.
- NIST SP 800-218 SSDF v1.1: prepare, protect, produce well-secured software, and respond to vulnerabilities.
- CISA Secure by Design 2025 guidance: secure defaults, customer security outcomes, memory-safe language roadmaps, and avoiding known product security bad practices.

## 2026 Threat Model Emphasis

Foundation modules must assume active abuse of APIs, automation, identity edges, and dependency supply chains:

1. **Access-control failures remain the dominant app risk**: every route, event, worker, object read, and state transition needs server-side action and object authorization.
2. **API abuse is business-logic abuse, not only malformed input**: rate limits, quotas, idempotency, cost caps, and state-machine checks belong in domain flows.
3. **Unsafe third-party API consumption is an ingress path**: partner responses, webhook callbacks, files, and AI/tool outputs must be validated like user input.
4. **SSRF reaches cloud metadata and internal control planes**: outbound URL validation must block private/link-local networks and re-check redirects.
5. **Supply-chain risk is continuous**: lockfiles, SCA/audit output, generated artifacts, CI scripts, and package publish flows require review and reproducible builds.
6. **Memory safety is a roadmap item**: new high-risk native code should prefer Go/Rust/WASM-safe boundaries, keep unsafe blocks exceptional, and test FFI buffer contracts.
7. **Security logging must be useful but not leaky**: log authz failures, validation anomalies, rate-limit events, and high-risk actions with correlation IDs while hashing or redacting secrets.

## Three-Pass Vulnerability Synthesis

### Pass 1: Entry Points and Trust Boundaries

Treat every browser, API, websocket, webhook, queue, object-storage callback, file upload, redirect target, URL fetch target, template value, and OAuth callback as attacker-controlled until proven otherwise.

High-risk boundary classes:

1. **Request routing**: query strings, route params, headers, cookies, content types, body encodings, and websocket envelopes.
2. **Identity and tenancy**: session cookies, bearer tokens, OAuth state, organization IDs, user IDs, roles, capabilities, and invite/reset tokens.
3. **Server-side interpreters**: SQL, templates, XML parsers, shell commands, file paths, image/video processors, archive extractors, and dynamic plugins.
4. **Network egress**: webhook delivery, URL preview, import/sync connectors, object-store endpoints, metadata services, redirects, and proxy-like features.
5. **State transitions**: payments, refunds, role changes, approvals, publish/unpublish, invitation acceptance, upload promotion, and worker retries.

### Pass 2: Vulnerability Families and Required Controls

| Family | Foundation control |
| --- | --- |
| Open redirect | Use `security.ValidateRedirectTarget`; allow relative same-origin paths or exact allowlisted absolute hosts only. Reject schemeless URLs, userinfo, CRLF/control chars, backslashes, and suffix matching. |
| HTTP parameter pollution | Reject duplicate security-sensitive params. Foundation HTTP dispatch rejects duplicate GET/DELETE query keys by default. |
| CSRF and cross-site websocket abuse | Do not mutate on GET. Cookie-authenticated mutations and websocket upgrades validate `Origin`; cookies use `Secure`, `HttpOnly`, and `SameSite`; CSRF tokens remain required for browser cookie flows where origin alone is insufficient. |
| HTML injection and XSS | Prefer framework escaping, context-specific encoders, no raw HTML sinks without sanitizer allowlists/tests, strict CSP, and token storage that minimizes XSS blast radius. |
| CRLF/response splitting | Reject control characters in response headers, redirects, cookies, filenames, proxy headers, and signed URL metadata. |
| SQL injection | Parameterize values, allowlist identifiers, avoid string-built predicates, and keep tenant predicates inside the same query/transaction. |
| SSRF | Use `security.ValidateOutboundURL`; require `https` by default, exact host allowlists, DNS/IP private-network blocking, bounded timeouts, and re-validation across redirects. |
| XXE | Disable external entities/DTDs; prefer JSON/protobuf/Cap'n Proto. XML ingestion requires parser configuration tests and strict size/depth limits. |
| RCE and command injection | Avoid shell execution with user input. Use argv arrays, allowlisted commands, fixed working directories, dropped privileges, timeouts, and bounded output capture. |
| Memory/file parser flaws | Limit upload size, type, archive depth, decompressed size, image dimensions, and processing time. Store untrusted bytes outside executable roots. |
| Subdomain takeover | Keep DNS/CNAME inventory, remove DNS before deprovisioning third-party services, avoid high-value wildcard cookies, and monitor dangling records. |
| Race conditions | Put invariants in database constraints, locks, idempotency keys, and serializable/atomic transitions. Re-check actor authority and state inside the transaction. |
| IDOR/BOLA | Derive tenant from authenticated context; authorize both action and target object; use opaque IDs only as defense in depth. |
| OAuth | Bind `state` to session, action, redirect, nonce, and short TTL; exact-match redirect URIs; use PKCE for public clients; never log or persist raw tokens unnecessarily. |
| Logic/configuration flaws | Test negative business paths, default-deny config, disabled debug routes, least-privilege credentials, explicit CORS, and production-safe error messages. |
| Resource consumption | Cap request bodies, response bodies, upload size, decompressed size, pagination, retry counts, concurrent work, and paid third-party actions. |
| Unsafe third-party API consumption | Validate schemas, enum values, MIME/types, signatures, freshness, redirect targets, and size limits on all partner responses and callbacks. |
| Supply-chain compromise | Keep lockfiles reviewed, run package audits/SCA, pin generated toolchains where practical, protect CI secrets, and treat install scripts as code execution. |
| Exceptional-condition mishandling | Test parser errors, timeouts, partial writes, oversized responses, downstream failure, and logging failure so errors fail closed without leaking secrets. |

### Pass 3: Regression Test Matrix

Every exposed feature should add tests for the vulnerability families it touches:

1. **Auth/session**: duplicate params, CSRF rejection, state mismatch, token replay, token expiry, cookie flags, role downgrade, logout/revocation.
2. **Tenant data**: cross-org object access, mass assignment of owner/org/role fields, missing object authorization, pagination/filter leakage.
3. **Redirect/OAuth**: schemeless target, suffix host, userinfo host, control chars, stale state, redirect URI mismatch.
4. **Outbound fetch/webhooks**: loopback/private/link-local/metadata addresses, DNS rebinding-style resolver changes, redirect to disallowed host, timeout enforcement.
5. **Uploads/files**: traversal, absolute paths, extension spoofing, MIME mismatch, oversize bodies, archive expansion, executable storage path.
6. **Rendering/templates**: unsafe HTML sink, context mismatch, CSP regression, template expression injection.
7. **State transitions**: double submit, concurrent approval/refund/invite acceptance, idempotency replay, worker retry after partial failure.

## Required Implementation Defaults

1. Use `server-kit/go/security` helpers for redirect, outbound URL, duplicate query parameter, and path containment checks.
2. Use `security.CSRFProtection` for browser cookie mutation surfaces on Go 1.26+ and keep explicit origin checks for websocket upgrades.
3. Use resilient HTTP clients with encoded query construction, bounded response bodies, per-call timeouts, and outbound URL policy for partner/webhook egress.
4. Reject ambiguous input early with foundation `domainerr.Validation` errors.
5. Prefer exact allowlists over blocklists. Host suffix checks are not authorization.
6. Enforce bounded operations on every external call: lookup, connect, TLS handshake, request, read, retry count, and worker attempts.
7. Keep secrets out of query strings, logs, analytics, crash reports, and client-readable storage.
8. Treat production security headers, CORS, origin validation, rate limiting, and content-type enforcement as middleware baselines, not optional route features.
9. Preserve an inventory of exposed routes, API versions, queue topics, webhook receivers, public buckets, DNS records, and package entrypoints.
10. Generated production scaffolds must default to authentication enabled, exact allowed origins, and protected operational endpoints. `/metricsz`, `/metricsz/trace`, and operational event views are not public production surfaces.

## Review Checklist

- [ ] Does the change introduce a new entry point or trust boundary?
- [ ] Are duplicate query/form keys rejected or canonicalized with an explicit reason?
- [ ] Are redirect and outbound URL hosts exact-match allowlisted?
- [ ] Can any attacker-controlled value reach SQL, templates, shell commands, XML, filesystem paths, headers, cookies, or logs?
- [ ] Is object-level authorization performed after deriving tenant from authenticated context?
- [ ] Are state changes idempotent and protected against concurrent replay?
- [ ] Are timeouts, size caps, and retry limits explicit?
- [ ] Do tests include at least one negative case for each touched vulnerability family?
- [ ] Does every security-relevant rejection produce a non-secret, correlation-friendly log or error signal?
- [ ] Are dependency, generated-code, and CI changes covered by audit/SCA or reproducible verification?
