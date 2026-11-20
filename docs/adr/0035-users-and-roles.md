# ADR-0035 Users and roles

**Status:** Proposed. **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0010 (v1 scope, no reseller), ADR-0018 (API, scopes, "a token never grants more than its owner's role"), ADR-0032 (SSH per site), [`03-features.md`](../03-features.md) row "Users + roles (admin, site manager, user per site)" (P0), [`05-security.md`](../05-security.md) 5.4 (RBAC with site grants, IDOR matrix).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- The panel has exactly one account. `POST /api/v1/setup` creates it with the role `admin`, and nothing else can create a user [V: `internal/store/users.go` `CreateFirstUser`, `internal/serve/auth_api.go`].
- The column `users.role` exists, but its `CHECK` allows only `'admin'` [V: `internal/store/migrations/0002_auth.sql`]. `users.disabled` exists; a disabled user's sessions and tokens stop working [V: `SessionUser`, `APITokenByHash`].
- The routes that a token may call go through a small `admin` wrapper that each file defines for itself, 16 copies of `p.User.Role != "admin"` in all [V: `grep` over `internal/serve`]. The session-only routes check no role: token and passkey management concern the caller's own account, but the API IP allowlist (`PUT /api/v1/settings/api-ip-allowlist`) is a server setting that only works because every account is an admin [V]. With a second role, each copy must be changed by hand and nothing notices a forgotten one [A].
- API tokens carry scopes, and every route that a token may call already names its scope through `authn.scoped` [V: `internal/serve/tokens_api.go`]. Token management, account security and the IP allowlist need a browser session [V].
- ADR-0018 promises tokens limited to individual sites; that is not built yet [V].
- The audit log stores `user_id` with `ON DELETE SET NULL`, so deleting a user would erase who did what [V: `0001_init.sql`].
- Kim runs the panel for their own servers and for customers of Maki IT [V: project memory]. Customers want to manage their own site, not the server [A].
- v1 has no reseller and no billing (ADR-0010) [V].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Fixed roles `admin`, `manager`, `user` plus site grants for `user`** | Matches `03-features.md` and the CloudPanel reference. One fixed permission table that a test can cover completely. A `manager` is cheap once `user` exists | Three roles to test instead of two [E: the matrix test is generated, so little extra work] |
| B. Only `admin` and `user` with site grants | Smallest change | A colleague who looks after all sites must be an admin, including firewall and users, or be granted every new site by hand [A] |
| C. Free roles built from scopes (permission sets per user or per grant) | Very flexible | A large surface for mistakes (a forgotten scope opens a server setting), a UI nobody needs in v1, much harder to test [A]. Not boring |

## Recommendation

**Option A.**

### Roles

| Role | Sees | May |
|------|------|-----|
| `admin` | everything | everything, including users, server settings and other admins |
| `manager` | all sites | everything that belongs to a site, on every site, including creating and deleting sites; no server settings and no users |
| `user` | only sites granted to them | the day-to-day work on their granted sites (table below); cannot create or delete sites |

What counts as a **server setting** (admin only): users and grants, firewall, backup target and settings, panel TLS, API IP allowlist, metrics, limit presets, the CloudPanel migration, doctor, `POST /api/v1/web/sync`.

What a `user` may do on a granted site [A, see open question 3]:

| Area | `user` | Why |
|------|--------|-----|
| Read the site, its databases, cron jobs, SSH keys, limits, backups | yes | |
| Maintenance mode, PHP version and settings, HSTS, cron jobs, SSH keys (SFTP and shell) | yes | All run as the site's own Linux user, inside the site's isolation and limits (ADR-0007, ADR-0015, ADR-0032) |
| Databases of the site (create, users, passwords, delete, import, export) | yes | Each database user reaches only its own database (ADR-0024) |
| Restore a backup of the site | yes | Writes only into the site's home (ADR-0008) |
| Domains and TLS of the site | no | A domain is a claim on a name: a user could take a name another customer needs or trigger ACME rate limits for the whole server [A] |
| Resource limits | read only | Otherwise a user raises their own quota |
| Custom nginx snippets | no | nginx runs for all sites; the allowlist reduces, but does not remove, the risk (ADR-0029) |
| Disable or delete the site | no | |

A `manager` may also do the "no" rows except snippets, which stay admin-only for the same reason.

### Site grants

- New table `site_grants (user_id, site_id)`, only for the role `user`. A grant is all or nothing for the site; there are no per-grant permissions in v1.
- A site without a grant is **invisible**: lists leave it out, and a request for it answers `404`, the same as an unknown site, so names cannot be probed [A].
- Deleting a site deletes its grants (`ON DELETE CASCADE`). Changing a user's role away from `user` deletes the user's grants.

### Enforcement in one place

- A new package `internal/authz` holds the closed table "action → roles" and one function `Check(principal, action, site)`. Every handler calls it; no handler compares role strings itself.
- The action names reuse the scope names (`sites:write`, `cron:write`, ...), plus a few that have no token scope (`users:write`, `settings:write`). So a token check and a role check speak the same language.
- **A token never grants more than its owner.** The owner's role and grants are read on every request, not copied into the token, so a demoted or disabled user's tokens shrink or stop at once. The token dialog only offers scopes the owner's role has.
- Optional site list per token (promised in ADR-0018) comes in the same table: a token may name sites, and then reaches only those, and only if the owner still reaches them.
- **Route matrix test.** A test lists every route of the mux and fails if one has no entry in the table. For each route and each role (admin, manager, user with and without a grant, token with and without the scope) it checks the answer: allowed, `403`, or `404`. That is the IDOR matrix of `05-security.md` [A: built from the mux, so a new route cannot be forgotten].

### Accounts

- Only an admin creates, changes and deletes users and grants.
- **Invitation instead of a password chosen by the admin.** Creating a user returns a one-time link, valid for 24 hours, where the new user sets their own password and second factor. Only the SHA-256 of the link's token is stored, as for sessions [A, see open question 4].
- **Second factor** (TOTP or passkey) is required for `admin` and `manager`: without one, the account can only enroll one after login and do nothing else [A, see open question 2]. For `user` it is optional, an admin can require it per user.
- **No lockout of the last admin.** The panel refuses to demote, disable or delete the last enabled admin, decided inside one write transaction. Nobody changes their own role.
- A role change, a disable or a password reset ends the user's sessions.
- An admin can reset another user's password and second factor; both return a new invitation link and land in the audit log.
- **Root recovery.** `hostashi user recover <name>` on the server (as root, through the agent socket) prints a new invitation link, for when the last admin has lost every factor [A].
- **Audit log keeps the name.** A new column `actor` stores the username at the time of the action, so deleting a user keeps the history readable.

### API

`GET/POST /api/v1/users`, `GET/PUT/DELETE /api/v1/users/{id}`, `PUT /api/v1/users/{id}/grants`, `POST /api/v1/users/{id}/reset`, `POST /api/v1/invitations/{token}` (public, rate limited). User management needs a browser session, like token management; no token scope can change users [A: an admin's token that leaks should not create admins].

## Consequences

- Migration: `CHECK (role IN ('admin', 'manager', 'user'))` (SQLite needs a table rebuild for a changed `CHECK`), `site_grants`, `invitations`, `audit_log.actor`, token site list. The existing account stays `admin`.
- Every handler moves to `authz.Check`; the 16 local `admin` wrappers disappear. Lists (sites, databases, cron, SSH keys, backups) get a filter by grants.
- Panel pages "Users" (admins only), an invitation page, the role in the top bar, menus that hide what the role cannot do (the API still decides).
- `05-security.md` 5.4 and `03-features.md` row "Users + roles" are covered for v1.
- Work in small PRs [E]: 1. `internal/authz` and the route matrix test with today's single role, no change in behavior; 2. store: roles, grants, invitations, actor; 3. users API with invitations and last-admin guard; 4. roles and grants enforced in all handlers and lists; 5. token site list and scopes by role; 6. panel pages; 7. `hostashi user recover`.

## Risks to name openly

- **A forgotten check is a data leak between customers.** The route matrix test is the main protection; it only tests what the table says, so the table itself needs review [A].
- **Shell access is a lot of power.** A `user` with a shell key runs anything as the site's Linux user. The site isolation (ADR-0007, ADR-0033 H8) is what keeps that inside the site, not the panel's roles [A].
- **Invitation links in email or chat** can be read by others. 24 hours, one use, and the second factor that follows limit the damage [A].
- **Not a pentest.** The matrix test checks what we thought of; an external review is still open.

## Out of scope for v1

Custom roles, per-grant permissions, users who create users (reseller, ADR-0010), OIDC/SSO (`03-features.md` P2), per-user quotas, email delivery of invitations (the admin passes the link on).

## Open questions

1. Three fixed roles `admin`, `manager`, `user` with site grants (option A)? **Recommendation: yes.**
2. Second factor required for `admin` and `manager`, optional for `user` (an admin can require it per user)? **Recommendation: yes.**
3. A `user` may not change domains, TLS, limits or snippets of their site, nor disable or delete it? **Recommendation: yes.**
4. New users come in through a one-time invitation link instead of a password the admin sets? **Recommendation: yes.**
