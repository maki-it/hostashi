# ADR-0018 Public REST API design

**Status:** Accepted (2026-11-18, by Kim). **Date drafted:** 2026-11-18. **Decides:** Kim. **Related:** ADR-0004 (web/agent split), ADR-0003 (stack), ADR-0006 (state and reconciler), ADR-0017 (presets and custom snippets).

## Context

- Kim wants a REST API to control the panel, for example to switch a site's maintenance or parking page on and off, "among many other things" [V: Kim, thread, 2026-11-18].
- The feature matrix already lists a REST API (OpenAPI 3.1) as P0 / Alpha, states that the UI uses the same API, and lists API tokens with scopes and expiry as P0 [V: `03-features.md` 3.6, 3.10]. The architecture names REST + OpenAPI 3.1 with SSE for streaming, and gRPC only internally between `serve` and `agent` [V: `04-architektur.md` 4.9].
- The security model says: the web process runs without root and can only ask the agent for typed operations; every handler checks role and ownership; tokens are stored as hashes and shown once [V: `04-architektur.md` 4.2, `05-sicherheit.md`].
- What is not written down yet: versioning, how resources are addressed, how long-running work is reported, how scopes look, and the rule that every UI function must exist in the API. This ADR fills those gaps. No code exists yet.

## Options

| Option | Summary | Assessment |
|--------|---------|------------|
| A. REST + OpenAPI 3.1, API-first | One public, versioned API. The SPA and the CLI are clients of it. | Boring, well understood, easy to script with `curl`, good tooling for a typed TypeScript client [U]. |
| B. GraphQL | One flexible query endpoint. | Harder to authorize per field, harder to rate limit and to cache, more unusual for operators [U]. Not worth it for a single-server panel. |
| C. gRPC or Connect as public API | Reuse a typed RPC style outside the server. | Awkward from shell scripts and webhooks, browser support needs a proxy or Connect [U]. Keeps the internal agent protocol closer to the public surface, which we do not want. |
| D. Internal UI endpoints, public API later | Build UI-shaped endpoints now, design a public API later. | Fastest at first, but leads to two APIs and features that exist only in the UI. This is exactly what Kim wants to avoid. |

## Recommendation

Option A, with these rules:

1. **API-first.** Every action in the UI goes through a documented public endpoint. No UI-only endpoints. The CLI uses the same API (or the agent directly when run as root on the server, as planned in `04-architektur.md` 4.1).
2. **Versioned base path** `/api/v1/`. Before release 1.0 breaking changes are allowed with a changelog entry. From 1.0 on, `v1` only changes in compatible ways; removals are announced with a `Deprecation` header for at least one minor release first [A].
3. **The OpenAPI 3.1 document is the contract** and lives in the repository. CI checks that the implementation and the document match, and the SPA's TypeScript client is generated from it. The exact Go tooling (spec-first generation or code-first with schema export) is chosen in the first API spike [A].
4. **Stable IDs, never domains, as resource keys.** `GET /api/v1/sites/{siteId}`. A domain is an attribute that can change (see "change a site's domain" in `03-features.md`).
5. **Declarative writes.** Most endpoints change the desired state in the database. The reconciler applies it (ADR-0006). The response returns the new state and a job reference. Long-running work returns `202 Accepted` with a `Job` resource; progress streams over SSE.
6. **Safe retries and concurrent edits.** `Idempotency-Key` header on `POST`. `ETag` and `If-Match` on resources that are edited, so two clients do not silently overwrite each other [A].
7. **Errors** as Problem Details (RFC 9457) with a stable `type` per error class, and field-level validation errors [U].
8. **Lists** use cursor pagination and fixed filter parameters, no free-form query language.

### Authentication and authorization

- **Browser:** session cookie (`HttpOnly`, `Secure`, `SameSite=Strict`) plus CSRF token, as in `05-sicherheit.md`.
- **Automation:** bearer API tokens. Each token has an owner, an expiry date (required, maximum lifetime configurable), and scopes. Only a hash is stored, the token is shown once.
- **Scopes** are `resource:action` pairs, for example `sites:read`, `sites:write`, `sites.maintenance:write`, `backups:restore`. A token can optionally be limited to a list of site IDs. A token never grants more than its owner's role allows; each request checks token scope **and** owner RBAC **and** resource ownership.
- High-risk actions (delete site, restore, user and token management, firewall) need an explicit scope and are never covered by a wildcard [A].
- Every write is written to the audit log with the actor, the token ID if any, and the diff.
- Login and token use are rate-limited; failed token checks are logged without the token value.

### Reachability

The API is served on the panel's own address and port, no separate listener. An optional IP allowlist restricts bearer-token access; browser sessions are not affected by it [V: Kim, thread, 2026-11-18].

### Security boundary

- The API is served by the web process, which runs without root (ADR-0004). It can only change desired state or call **typed agent operations**. No endpoint accepts a shell command, a command line, or a free path outside the site's root.
- File endpoints (file manager, maintenance and parking pages) go through the agent's confined file operations (Spike S4).
- **Named conflict:** custom nginx snippets (ADR-0017) are free text that the API accepts and stores. They are validated by `nginx -t` and the S2 candidate check, but not judged for safety. The API therefore requires a separate high-risk scope for snippet writes.

### Example: maintenance and parking pages

```text
GET    /api/v1/status-pages                 list stored HTML pages (maintenance, parking)
POST   /api/v1/status-pages                 upload a page (HTML plus assets, size-limited)
PATCH  /api/v1/sites/{siteId}               {"maintenance": {"enabled": true, "pageId": "...", "allowIps": ["203.0.113.4"]}}
PUT    /api/v1/parked-domains/{domain}      park a domain without a site, with a chosen page
```

The shape is illustrative; the final resource names come with the spec.

## Consequences

- Each feature costs a bit more up front: endpoint, schema, scope, RBAC test, audit entry. In return, scripting, the CLI and later integrations (Terraform/OpenTofu, `03-features.md` 3.10) need no extra work.
- The IDOR test matrix from `05-sicherheit.md` runs over the OpenAPI document: every route, every role, every token scope.
- The public API is a compatibility promise from 1.0 on. Internal refactoring must keep `v1` stable.
- The SPA cannot take shortcuts. If the UI needs data in a new shape, the API gets a new endpoint or field for everyone.
- Billing integrations such as WHMCS stay out of scope for v1 (non-goal), but could be built by third parties on this API.

## Open questions

Accepting this ADR settles the recommendation. Question 2 is still open and is not decided by the acceptance.

1. ~~Should the API be reachable on a separate address or port, or on the panel's address?~~ Decided by Kim (2026-11-18): same address as the panel, with an optional IP allowlist for token access.
2. Default maximum token lifetime: suggestion 90 days, with an explicit "no expiry" only for the owner role and a warning [E].
3. Outgoing webhooks (events such as "backup failed") belong to notifications (`03-features.md` 3.8) and are not part of this ADR.
