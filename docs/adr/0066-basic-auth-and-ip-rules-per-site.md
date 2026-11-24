# ADR-0066 Password protection and IP rules per site

**Status:** Accepted (Kim, 2026-11-24). **Date drafted:** 2026-11-24. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0005 (nginx), ADR-0018 (API), ADR-0021 (fail2ban), ADR-0027 (maintenance pages), ADR-0029 (nginx snippets), ADR-0035 (roles), ADR-0046 (page cache), ADR-0053 (site logs), ADR-0054 (site types), ADR-0056 (security headers), ADR-0058 (OpenAPI and CLI), ADR-0059 (real client IP).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- `03-features.md` lists "IP/bot blocking, basic auth per site" as P1, and the chosen P1 rows of the v1 check name "basic auth and IP blocking per site as a switch" [V]. Bot blocking is not part of this ADR.
- Today the only way is a custom snippet (ADR-0029): its allowlist already accepts `allow`, `deny`, `auth_basic`, `auth_basic_user_file` and `satisfy`, with the user file as a literal path below the site's home [V: ADR-0029, `internal/snippet`]. The admin must create the password file by hand, in the site's home, where the site's own processes can change it, and only an admin may edit snippets [V: ADR-0035].
- nginx checks access in this order: `allow`/`deny` (module `access`), then `auth_basic`; `satisfy any` lets either one pass [U: nginx docs]. Both inherit into a location only if the location sets none of its own [U]. A server-level `deny all` would also block the site's ACME challenge location and the agent's health check, unless those locations set their own rules [V: sandbox test below].
- `auth_basic` takes variables, and a value of `off` switches it off for that request [V: sandbox test, nginx 1.24 on Ubuntu 24.04]. So a `map` on `$uri` can protect only some paths without copying locations, which matters because a PHP site would otherwise need its own PHP location inside every protected path.
- The password file is read by the nginx **workers** (user `www-data`) on every request, not by the root master [V: sandbox, a 0600 file gives 500 with "Permission denied"]. The agent's generation directory is root 0700 [V: `internal/web/reconciler.go`], so the file cannot live there. Site users are not in group `www-data` (own group plus the SSH group) [V: `internal/sysuser`].
- nginx checks the password with the system `crypt()` and supports `$apr1$`, `{SHA}` and whatever libxcrypt supports, including bcrypt (`$2a$`/`$2b$`/`$2y$`) and SHA-512 crypt (`$6$`) [V: sandbox, all four accepted]. The check is synchronous inside the worker, so its cost stalls every other request of that worker [U]. Measured per request in the sandbox: bcrypt cost 10 about 50 to 100 ms, bcrypt cost 5 about 3.5 ms, SHA-512 crypt (5000 rounds) about 3 ms, apr1 about 0.5 ms [V: sandbox, one core, rough]. `golang.org/x/crypto/bcrypt` is already a dependency [V: `go.mod`].
- The realip module (ADR-0059) replaces the client address before the access phase, so `allow`/`deny`, `geo` and the error log all see the visitor's real address once a site trusts its proxy [V: sandbox, `set_real_ip_from` + `X-Real-IP`, the error log says "access forbidden by rule, client: <real address>"]. Without that setting, a site behind Cloudflare sees only Cloudflare's addresses, so an IP rule would block or allow the CDN as a whole [A].
- nginx writes "password mismatch", "user ... was not found" and "access forbidden by rule" to the site's error log (ADR-0053) [V: sandbox]. fail2ban ships a filter `nginx-http-auth` for the first two [U]. The site error logs are `/var/log/nginx/sites/<site>/error.log` [V: `internal/agent/agent.go`].
- With basic auth, the browser sends `Authorization` on every request, and nginx passes it on to PHP (`HTTP_AUTHORIZATION`) and to proxied targets [U]. A `proxy` site's target may be another server, so it would receive the site's password.
- `$uri` is decoded and normalized (dot segments, double slashes) before `map` sees it, but it is case-sensitive [U]. Many PHP applications route case-insensitively or through `index.php` [A].
- CloudPanel offers basic auth and IP blocking per site; only its feature list is used here (ADR-0001) [V: `01-cloudpanel-research.md`].

## Options

**How the setting is made**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Nothing new, admins use snippets | No code | Admin only, password file by hand inside the site's home, no switch, no user list |
| **B. Typed settings per site: an IP rule and a list of basic auth users, rendered by Hostashi** | Switch in the panel, hashes made by the panel, file outside the home, works for every site type, checked twice (panel and agent) | Hostashi must keep the ACME and health locations open and keep snippets from mixing in |
| C. Free list of `allow`/`deny` lines in order, like nginx | Most flexible | Easy to get wrong (order matters), hard to show in a panel |

**Protecting only some paths**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Whole site only | Simplest | `/wp-admin/` only, a common wish, needs a snippet [A] |
| **B. Whole site or a list of path prefixes, through `map $uri` and a variable `auth_basic` at the server level** | No copied locations, PHP keeps working below the path | Prefix list only, no regular expressions |
| C. One `location` per protected path | Plain nginx | PHP needs a nested PHP location per path, interacts with snippets and the page cache |

**Password hash**

| Option | Pro | Contra |
|--------|-----|--------|
| A. apr1 (MD5) | Fastest, every nginx | Weak hash |
| **B. bcrypt with cost 5** | Library already in use, salted, much stronger than apr1 | About 3.5 ms per protected request inside the worker |
| C. bcrypt with the library default (10) | Strongest | About 50 to 100 ms per request, stalls the worker; a page with 30 files costs seconds |

## Recommendation

**Option B in all three tables.**

### What a site can set

- **IP rule:** off, *only these addresses* (others get `403`) or *block these addresses*. Up to 256 entries, each an address or CIDR range, parsed like the real IP ranges (ADR-0059). In "only these addresses" mode the list must not be empty. No ordered mixed list.
- **Password protection:** off, *whole site* or *only these paths* (up to 20 prefixes, each starting with `/`, characters `A-Z a-z 0-9 . _ ~ / -` only, at most 200 bytes). A prefix covers the path itself and everything below it (`/wp-admin` covers `/wp-admin` and `/wp-admin/x`, not `/wp-administrator`), matched **case-insensitively**, so `/WP-Admin/` cannot get around it. The realm text shown by the browser is fixed ("Restricted").
- **Users:** up to 50 per site. Name 1 to 64 characters `A-Z a-z 0-9 . _ @ -`. Password 12 to 72 bytes, printable characters only, or generated by the panel (24 characters, shown once). The panel stores only the bcrypt hash (cost 5); the API never returns it. Password protection cannot be switched on without at least one user.
- **No password from these addresses** (optional): up to 64 addresses or ranges that skip the password, for an office or an uptime monitor. Refused: loopback and the server's own addresses (`CheckOwn` as in ADR-0059), because every site user on the server could connect from those.

### nginx rendering

- **IP rule** at the server level of every server block that serves the site: `allow` per entry then `deny all`, or `deny` per entry then `allow all`. The redirect-only HTTP block gets it too, so a blocked address learns nothing.
- **Password** through a `geo` (bypass list) and a `map` on `"$bypass$uri"` that yields `off` or the realm, in the site's file at the `http` level as the maintenance `geo` is today; `auth_basic $realm;` and `auth_basic_user_file` at the server level. Whole site means `default` is the realm.
- **Always open:** the ACME challenge location and `/.hostashi/health` set `allow all; auth_basic off;`, so certificates renew and the agent's check after a reload works whatever the site sets.
- **No password to the application:** for requests that nginx checked, `Authorization` is not passed on (`fastcgi_param HTTP_AUTHORIZATION ""` for PHP, `proxy_set_header Authorization ""` for `node`, `app`, `python` and `proxy`), through the same `map` so unprotected paths keep the visitor's own header. An application that does its own basic auth on a protected path cannot see it; the help text says so.
- **Password file:** written by the agent to `/var/lib/hostashi-web-auth/<site>-<sha256 prefix>.htpasswd`, root:www-data 0640, directory root:www-data 0750. The name follows the content, so a rolled-back generation still points at its own file; after each successful reload the agent removes the files no live site uses (implementation note, 2026-11-24: the directory is its own, because the panel's `/var/lib/hostashi` is hostashi 0750 and the agent's state is root 0700). The agent writes `name:hash` lines itself from checked values, never text from the panel.
- **Maintenance, disabled, page cache:** the rules apply to every location of the site, including maintenance and disabled pages; the access phase runs before the page cache answers [U], so cached pages are protected as well.
- **Snippets:** a snippet with `allow`, `deny`, `auth_basic`, `auth_basic_user_file` or `satisfy` while the matching setting is on is refused (`422 access_conflict`), in both directions, as for headers (ADR-0056). Existing snippets with these lines keep working while the settings are off.

### fail2ban

A jail `hostashi-site-auth` with fail2ban's `nginx-http-auth` filter (implementation note, 2026-11-24: over nginx's server error log `/var/log/nginx/hostashi/error.log`, which protected sites write to as well; a glob over the site logs would miss sites created after fail2ban started), 10 failures in 10 minutes, ban on ports 80 and 443 only (a mistyped site password should not lock a customer out of SSH). Trusted proxy ranges are already in `ignoreip` (ADR-0059). Behind a CDN the ban blocks the real address on the firewall, which has no effect because the connection comes from the CDN; the panel says so next to the setting.

### Panel, API, roles, storage

- Site settings get a card "Access protection" with the IP rule, the password switch with paths, the bypass list and the user list. It shows the caller's own address and warns when an "only these addresses" list does not contain it, and when the site has no real IP setting while its traffic seems to come from a CDN (hint only). UI as screenshots first.
- API: `GET`/`PUT /api/v1/sites/{name}/access` for the rule, paths and bypass list (`PUT` replaces the whole set); `GET`/`POST /api/v1/sites/{name}/access/users`, `PUT` (new password) and `DELETE /api/v1/sites/{name}/access/users/{user}`. `POST` with `"generate": true` returns the new password once. All routes through `mux.Op` with OpenAPI (ADR-0058); the CLI gets them for free. Errors: `422 access_rejected` with the reason, `422 access_conflict`.
- Roles: action `sites.access:write` for everyone with a grant for the site, like headers and the real IP preset, because it changes only that site (ADR-0035). No new token scope: `sites:write` covers writes, `sites:read` covers `GET`.
- Storage: migration 0061 with `site_access` (one row per site, the settings as a JSON object checked by `json_valid`; no row means everything off) and `site_auth_users` (site, name, bcrypt hash, created and changed time, who changed it). The panel database is already in the backup.
- Agent: `web.Site` gets a typed `Access` struct; `web.apply` checks it again (entries, paths, names, hash format `$2a$`/`$2b$`/`$2y$` with cost 4 to 12) and renders it. No new agent operation and no new `os/exec` exception; `nginx -t` with rollback as today.
- Audit log: every change with old and new settings; for users only the name and "password changed", never a hash.

### Not in this ADR

Bot blocking and rate limits per site, country blocking (needs a GeoIP database), regular expressions in paths, other hash algorithms, importing CloudPanel's basic auth users (their snippets keep working through ADR-0029), and the same switches for the panel itself (it has its own allowlist, ADR-0018/0042).

## Consequences

- Admins, managers and site users can put a site or `/wp-admin` behind a password or an address list in a few clicks, without snippets.
- Each protected request costs about 3.5 ms of worker time for the bcrypt check [V: sandbox]. On a whole-site password with many files per page this adds up; the help text recommends protecting only the admin paths of busy sites.
- bcrypt cost 5 is weaker against offline guessing than cost 10. The file is readable only by root and `www-data`, and generated passwords have about 140 bits, so the risk sits with short own passwords [A].
- IP rules without a real IP setting behind a CDN block or allow the CDN, not the visitor; the panel hints at it, but cannot know every proxy.
- Applications on protected paths no longer receive the `Authorization` header.
- Tests: checker tests with injection attempts (newline, `;`, `{`, `$`, quotes, colon in names, paths with `..` or `%`, too wide or too many ranges, loopback and own addresses in the bypass list, malformed hashes); template tests for every site type with and without certificate, maintenance and disabled; a container test with real nginx for 401/200 on a protected path, 403 by rule, open ACME and health locations, real IP behind a trusted proxy, and no `Authorization` at a PHP and a proxy target; snippet conflict tests in both directions.
- Work estimate: 5 to 6 small PRs (checker and agent rendering, store and API, fail2ban jail, panel, wiki) [E].

## Open questions for Kim

None left. Kim accepted the recommendation as drafted on 2026-11-24, including bcrypt cost 5, path prefixes, the bypass list and that site users (role `user`) may set all of this for their own sites.

## Sandbox test (2026-11-24)

nginx 1.24.0 on Ubuntu 24.04 [V]: `auth_basic $var` with `map` value `off` switches auth off; `map "$bypass$uri"` with `geo` protects one prefix and lets a bypass address through; bcrypt (`$2a$` cost 5 and 10), `$6$` and `$apr1$` hashes all accepted, wrong password 401, unknown user 401; server-level `deny all` blocks the ACME location unless it sets `allow all; auth_basic off;`; `satisfy any` with `allow` lets the address through without a password; `set_real_ip_from` + `real_ip_header X-Real-IP` makes `allow`/`deny` and the error log use the forwarded address; `allow all` in the health location keeps the `unix:` socket check working.
