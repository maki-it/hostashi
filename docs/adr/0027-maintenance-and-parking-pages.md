# ADR-0027 Maintenance and parking pages

**Status:** Proposed. **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0006 (state store and reconciler), ADR-0018 (API, example "maintenance and parking pages"), [`03-features.md`](../03-features.md) row "Disable a site, maintenance and parking pages" (P1), PR #93 (disable a site).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- The feature row asks for custom HTML pages stored once in the panel and switched on per site (UI, API, CLI), parking pages also for domains without a site, and optional access for allowed IPs during maintenance [V: `03-features.md`].
- "Disable a site" is done (PR #93): the site answers every request with a fixed 503 page and its PHP pool stops [V: `internal/web/nginx.go`]. Kim chose 503 over closing the connection (2026-11-19).
- The agent renders the nginx files from typed state; nothing a user typed becomes nginx syntax [V: `internal/web`]. One agent message is at most 64 KiB [V: `agentrpc.MaxFrame`].
- A page is HTML that the panel admin writes. Served on a site's own domain, a script in it could read that domain's cookies and act on the site for every visitor [U: same-origin rules]. The panel admin can already change any site, so this is no new privilege for the admin, but a page copied from somewhere else can carry a script the admin did not notice [A].
- Kim wants pages with their own files (CSS, JavaScript, images next to the HTML) and also the simple single HTML file (2026-11-19).
- Maintenance differs from "disabled": during maintenance the site must keep running (PHP on) so allowed IPs can work on it, for example a WordPress update [A: usual workflow].
- nginx can choose the response by client address without free text: `geo` blocks or `allow`/`deny` with an `error_page` [U: nginx docs], all rendered from validated IPs and CIDRs.

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| A. Pages as single HTML files in the panel, pushed to the agent, served with a strict Content-Security-Policy | No upload of archives. Fits in one agent message | Images only inline (`data:` URIs), no separate CSS, JavaScript or fonts, at most about 48 KiB. Does not meet Kim's wish for own files |
| **B. A page is a small archive (HTML plus CSS, JavaScript, images, fonts), checked by the panel, stored in the panel database and unpacked by the agent into a directory per page** | Normal web pages with a logo, fonts and stylesheets. The panel database stays the desired state and is in the backup | Needs a strict archive check (names, types, sizes, no links) and a chunked transfer to the agent, because one message is at most 64 KiB |
| C. Pages in the site's own web root (the site serves `maintenance.html`) | No panel storage | The page disappears with a broken deploy, which is exactly when it is needed. A parked domain has no web root |

## Recommendation

**Option B.**

**Pages**

- A page is uploaded either as a single HTML file or as a ZIP file (Kim, 2026-11-19: both). A single file is stored as a page with only `index.html`, so both take the same path from there on. Either upload is at most 2 MiB, unpacked at most 2 MiB [E: enough for a logo, a font and a stylesheet], with `index.html` at the top. It is stored in the panel database with a name and a kind (`maintenance` or `parking`). Hostashi ships one built-in page of each kind (single HTML, no scripts); they cannot be deleted.
- The panel checks the archive in Go (`archive/zip`, no external tool) before storing anything: at most 100 files and two directory levels, names `^[A-Za-z0-9_][A-Za-z0-9._-]{0,63}$` per part (no `..`, no hidden files, no absolute paths), only the types `html`, `css`, `js`, `png`, `jpg`/`jpeg`, `webp`, `gif`, `svg`, `ico`, `woff2`, no symlinks or other special entries, and the unpacked size is counted while reading, not taken from the header (zip bombs).
- The panel sends a page to the agent in chunks of at most 48 KiB: `page.begin {id}`, `page.write {id, path, offset, data}`, `page.commit {id, sha256}`. The agent writes into a new directory below `/var/lib/hostashi-agent/pages/` with `openat2` (no symlinks, no `..`), checks the hash and then swaps the page's directory in one rename, so a half-sent page is never served. `page.delete {id}`. `web.apply` names pages by id only. A page that is in use cannot be deleted.
- nginx serves the page's files under a reserved path `/.hostashi/page/<id>/` on the site's domain, also to addresses that are not allowed during maintenance, and the 503 answer is the page's `index.html`. Headers: `Content-Security-Policy: default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; form-action 'none'; frame-ancestors 'none'; base-uri 'none'`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`. Scripts run, from the page's own files and inline in its HTML, so a single-file page can carry its script too (Kim, 2026-11-19: JavaScript must be possible). Nothing loads from other hosts. The panel does not try to sanitize the files; the header and the fixed path are the boundary.
- SVG files are served as `image/svg+xml` under the same CSP, so scripts inside an SVG follow the same rule as the page [U: CSP applies to SVG documents opened directly].

**Maintenance per site**

- Settings: on/off, the page (default: built-in), and up to 20 allowed IPv4/IPv6 addresses or CIDRs.
- On: requests from other addresses get the page with status **503** and `Retry-After: 3600`. Allowed addresses see the site as usual. PHP keeps running. ACME challenges and the agent's health check are not affected, so certificates keep renewing.
- "Disabled" (PR #93) stays separate: no PHP, no exceptions. If both are on, disabled wins.
- The panel offers "add my current IP" (the address the panel sees, `X-Real-IP` from nginx, ADR-0023).
- API in the shape of ADR-0018: `GET/POST/PUT/DELETE /api/v1/status-pages[/{id}]`, `PUT /api/v1/sites/{name}/maintenance {"enabled", "page", "allowIps"}`. Scopes `sites:write` for maintenance and a new `pages:write` for the pages themselves.

**Parking**

- A parked domain is a domain without a site that shows a parking page with status 200. It gets a small vhost of its own, HTTP and, if wanted, HTTPS with a Let's Encrypt certificate under an agent name `_park-<n>` like the panel's own certificate (ADR-0023).
- A domain is either parked or belongs to a site, never both; turning a parked domain into a site removes the parking.
- API: `GET/PUT/DELETE /api/v1/parked-domains/{domain}`.

**Order of work:** pages and maintenance first (they serve Kim's own sites), parking second.

## Consequences

- New tables for pages, maintenance settings and parked domains; new agent operations `page.begin`, `page.write`, `page.commit`, `page.delete`; no new `os/exec` exception.
- The nginx templates get a maintenance branch per site (`geo` on the allowlist, `error_page 503` with the page) and a parked-domain template. The container test checks the 503, the page's files under `/.hostashi/page/`, the allowlisted address and the CSP header with a real nginx. The archive check gets fuzz and table tests (traversal, links, bombs, wrong types).
- **Named conflict:** a page's script runs on the site's own domain while the page is shown. It can read that domain's non-HttpOnly cookies and send requests to the site as the visitor. Accepted by Kim (2026-11-19) for countdowns and status widgets. Mitigations: only admins with the scope `pages:write` upload pages, every upload is in the audit log with its hash, the page lists its files in the panel, and the CSP stops loading code or sending data to other hosts.
- Pages are capped at 2 MiB; larger media do not fit and should be linked from the site itself after maintenance.
- The reserved path `/.hostashi/page/` is no longer free for the site's own content. The agent's health check already uses `/.hostashi/` [V: `internal/web/nginx.go`].
- Not in scope: an editor for pages in the panel (upload only), scheduled maintenance windows, maintenance on the panel itself.

## Open questions for Kim

1. ~~May the page's own JavaScript run?~~ Decided (Kim, 2026-11-19): yes, from its files and inline, see the named conflict.
2. Should parked domains get HTTPS with Let's Encrypt, or is HTTP enough for them? Recommendation: HTTPS. Without a certificate, `https://` on a parked domain hits the catch-all server that refuses the TLS handshake [V: `internal/web/nginx.go`], and browsers try HTTPS first more and more often [U].
