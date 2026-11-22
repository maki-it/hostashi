# ADR-0056 Security headers per site

**Status:** Accepted (Kim, 2026-11-22). **Date drafted:** 2026-11-22. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0005 (nginx), ADR-0017 (import and presets), ADR-0018 (API), ADR-0020 (nginx versions), ADR-0027 (maintenance pages), ADR-0029 (nginx snippets), ADR-0035 (roles), ADR-0046 (page cache), ADR-0047 (disabled sites), ADR-0054 (site types).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants security headers per site as switches in the panel for v1 (2026-11-22), for example `Content-Security-Policy`, `X-Frame-Options` and `Referrer-Policy` [V: project chat].
- Today a site gets only `Strict-Transport-Security` (switch `hsts`, one year, no `includeSubDomains`) and `Alt-Svc` for HTTP/3. Both are set at the server level of the HTTPS server block; the template says that locations set no `add_header` so that they keep these headers [V: `internal/web/nginx.go`, `PUT /api/v1/sites/{name}/hsts`].
- nginx inherits `add_header` from the enclosing level only if the current level has no `add_header` of its own [U: nginx docs of `ngx_http_headers_module`]. The same rule applies to `proxy_hide_header` and `fastcgi_hide_header` [U]. `add_header_inherit merge`, which would lift this, is newer than the nginx of Debian 13 (1.26.3) and Ubuntu 26.04 (1.28.3) [V: ADR-0020; U: directive added in 1.29.3].
- A site's `location /` snippet (ADR-0029) may contain `add_header`. Then that location already loses HSTS and `Alt-Svc` today [V: `internal/snippet/snippet.go` allows `add_header`, template includes the snippet inside `location /`]. Server snippets may also set headers, and CloudPanel sites often carry `X-Frame-Options` and `Referrer-Policy` in their vhost [V: `internal/cloudpanel/fixture`].
- Maintenance and disabled pages have their own location with a strict fixed CSP, `nosniff` and `no-store` [V: `pageLocSrc`]. They must keep that and not take a site's CSP.
- Applications often send some of these headers themselves (WordPress sends `X-Frame-Options: SAMEORIGIN` on its login and admin pages [U]). nginx `add_header` adds a second header instead of replacing it. Two different `X-Frame-Options` values are handled differently by browsers; two CSP headers are both enforced [U].
- Site types (ADR-0054): `static`, `php`, `node`, `app`, `proxy`, `redirect`. PHP answers come through FastCGI, `node`, `app` and `proxy` through `proxy_pass`, `redirect` sends only 30x [V: ADR-0054].
- `X-XSS-Protection` is obsolete and can do harm; modern browsers ignore it [U]. `Cross-Origin-*-Policy` headers break embedded content easily and are rarely asked for by Maki IT customers [A].
- A wrong CSP breaks a site at once (blocked scripts, styles, fonts). `Content-Security-Policy-Report-Only` lets an admin test a policy first [U].
- nginx strings: `$` starts a variable and `\` escapes inside quoted strings; a value with `"`, `;` outside quotes or a newline could end the directive and inject configuration [U]. The snippet checker already refuses control characters [V: `internal/snippet`].

## Options

**How the headers are set**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Nothing new, admins use snippets (ADR-0029) | No code | No switches, every admin writes header syntax, the inheritance trap stays |
| **B. Typed settings per site, rendered by Hostashi at the server level, repeated in every location that has its own `add_header`** | Switches and fixed choices, values checked twice (panel and agent), works for all site types, also fixes the snippet trap for HSTS | Hostashi must know every location that sets headers |
| C. Free key/value list of headers per site | Flexible | Same injection and inheritance problems as snippets, no guidance |

**When the application sends the same header**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Send both | Simple | Duplicate or conflicting headers, unclear result in the browser |
| **B. A header switched on in Hostashi replaces the application's (`fastcgi_hide_header` / `proxy_hide_header` for exactly that header)** | One clear value, what the panel shows is what the browser gets | An application that relies on its own stricter value loses it, so the panel says so next to each switch |
| C. Hostashi only adds a header if the application sent none | Application wins | Needs `map` per header on `$upstream_http_*`, more template logic, panel cannot say what is sent |

## Recommendation

**Option B for both.**

### Headers and choices

| Header | Choices | Default for new sites |
|--------|---------|------------------------|
| `X-Content-Type-Options` | off, `nosniff` | `nosniff` |
| `X-Frame-Options` | off, `SAMEORIGIN`, `DENY` | `SAMEORIGIN` |
| `Referrer-Policy` | off or one of the eight standard values | `strict-origin-when-cross-origin` |
| `Permissions-Policy` | off, preset "strict" (camera, microphone, geolocation, payment and usb off), own value | off |
| `Content-Security-Policy` | off, enforce, report only; with an own policy text | off |
| `Strict-Transport-Security` | stays the existing switch, unchanged | unchanged |

- **Existing sites and imports keep their behaviour:** everything off after the migration, and the CloudPanel import starts with everything off. Defaults apply only to sites created in the panel or API [V: Kim, 2026-11-22]. A later import step may turn known header lines of a CloudPanel vhost into switches; not in this ADR.
- **Own values** (`Permissions-Policy`, CSP) are checked in the panel and again in the agent: printable ASCII only, no `"`, `\`, `$`, `{`, `}` or control characters, at most 4096 bytes; CSP must look like `directive value...` separated by `;`, directive names from the CSP Level 3 list. Hostashi writes the value in double quotes itself. A check failure is `422 header_rejected` with the reason.
- **Not in v1:** `X-XSS-Protection` (obsolete), `Cross-Origin-*-Policy`, HSTS options (`includeSubDomains`, `preload`, other max-age), CSP nonces and reporting endpoints run by Hostashi, headers per path.

### nginx rendering

- All headers with `always`, so 4xx and 5xx answers carry them too.
- In the server block that serves the site: the HTTPS block when the site has a certificate, otherwise the HTTP block. The HTTP block that only redirects to HTTPS gets none (it answers with 301 only).
- All site types get them, `redirect` included (cheap and harmless on a 30x).
- For every switched-on header, `fastcgi_hide_header` (PHP) or `proxy_hide_header` (`node`, `app`, `proxy`) of the same name, so Hostashi's value replaces the application's. With the page cache (ADR-0046) cached answers are treated the same, because hiding happens when the answer is sent [A, the container test checks it].
- One template block "site headers" (Alt-Svc, HSTS, `X-Cache`, the security headers). It is rendered at the server level and again inside any location that sets its own `add_header` or hide header, today only `location /` with a snippet that contains `add_header` or `proxy_hide_header`. This also fixes the lost HSTS in that case. The maintenance and disabled page locations keep their own fixed set and never get the site's CSP.
- A snippet that sets one of the managed headers while its switch is on is refused (`422 header_conflict`), in both directions: saving the snippet and switching the header on. So nobody gets two values without noticing.

### Panel, API, roles, storage

- Site settings get a card "Security headers" with one row per header (switch or choice, short help text, link to MDN), a CSP text field with "report only" as the first choice, and a warning that a wrong CSP breaks the site. UI as screenshots first.
- API: `GET` and `PUT /api/v1/sites/{name}/headers` with typed fields; `PUT` replaces the whole set. Action `sites.headers:write` for everyone with a grant for the site, like HSTS and PHP settings, because the headers only change the site itself (ADR-0035). No new token scope: `sites:write` covers it, `sites:read` covers `GET`.
- Storage: migration 0048 with one table `site_headers` (one row per site, typed columns); no row means all off.
- Agent: `web.Site` gets a typed `Headers` struct; `web.apply` checks it again and renders it. No new agent operation, no new `os/exec` exception, `nginx -t` with rollback as today.
- Audit log: every change with the old and new values (as for every site setting).

## Consequences

- New sites send three common headers from day one; nothing changes for existing and imported sites until an admin switches them on.
- Applications that send their own stricter `X-Frame-Options` or CSP lose it when the same switch is on in Hostashi. The help text says so.
- A CSP switched to enforce can break a site; report-only first and an easy switch back limit the damage. The audit log shows who changed it.
- Tests: unit tests for the checker (injection attempts with `"`, `;`, `$`, newline, `{`, over-long values, unknown CSP directives), nginx template tests for every site type, with and without certificate, with a snippet that sets `add_header`, maintenance and disabled pages; the container test with a real nginx asks a PHP and a proxy site and checks that each header comes exactly once.
- Work estimate: 4 to 5 small PRs (checker and agent, store and API, nginx rendering, panel, wiki) [E].

## Open questions for Kim

None left.

1. ~~Should new sites get the three headers (`nosniff`, `SAMEORIGIN`, `strict-origin-when-cross-origin`) switched on by default, while existing and imported sites stay as they are?~~ **Decided (Kim, 2026-11-22): yes.**
