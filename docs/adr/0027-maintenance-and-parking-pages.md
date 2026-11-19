# ADR-0027 Maintenance and parking pages

**Status:** Proposed. **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0006 (state store and reconciler), ADR-0018 (API, example "maintenance and parking pages"), [`03-features.md`](../03-features.md) row "Disable a site, maintenance and parking pages" (P1), PR #93 (disable a site).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- The feature row asks for custom HTML pages stored once in the panel and switched on per site (UI, API, CLI), parking pages also for domains without a site, and optional access for allowed IPs during maintenance [V: `03-features.md`].
- "Disable a site" is done (PR #93): the site answers every request with a fixed 503 page and its PHP pool stops [V: `internal/web/nginx.go`]. Kim chose 503 over closing the connection (2026-11-19).
- The agent renders the nginx files from typed state; nothing a user typed becomes nginx syntax [V: `internal/web`]. One agent message is at most 64 KiB [V: `agentrpc.MaxFrame`].
- A page is HTML that the panel admin writes. Served on a site's own domain, a script in it could read that domain's cookies and act on the site for every visitor [U: same-origin rules]. The panel admin can already change any site, so this is no new privilege for the admin, but a page copied from somewhere else can carry a script the admin did not notice [A].
- Maintenance differs from "disabled": during maintenance the site must keep running (PHP on) so allowed IPs can work on it, for example a WordPress update [A: usual workflow].
- nginx can choose the response by client address without free text: `geo` blocks or `allow`/`deny` with an `error_page` [U: nginx docs], all rendered from validated IPs and CIDRs.

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Pages as single HTML files in the panel, pushed to the agent, served by nginx from a root-owned directory with a strict Content-Security-Policy** | No upload of directories or assets, no file manager needed. The page cannot run scripts or load anything from elsewhere. Fits the reconciler: the page is part of the desired state | Images must be inline (`data:` URIs) or absent. Size limit from the message size |
| B. Page plus assets uploaded into a directory per page | Normal web pages with separate CSS and images | Needs a confined upload and an archive check, more surface for little gain on a "we are back soon" page |
| C. Pages in the site's own web root (the site serves `maintenance.html`) | No panel storage | The page disappears with a broken deploy, which is exactly when it is needed. A parked domain has no web root |

## Recommendation

**Option A.**

**Pages**

- A page is one HTML document, at most 48 KiB [E: leaves room in one 64 KiB message], UTF-8, stored in the panel database with a name and a kind (`maintenance` or `parking`). Hostashi ships one built-in page of each kind; they cannot be deleted.
- The panel sends a page to the agent with a typed operation `page.put {id, html}` (id `^[a-z0-9-]{1,32}$`, content only as data). The agent writes it to `/var/lib/hostashi-agent/pages/<id>.html` (root, 0644, directory 0755) with `openat2`, and `web.apply` names pages by id only. A page that is in use cannot be deleted.
- nginx serves a page with `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; sandbox`, `X-Content-Type-Options: nosniff` and `Cache-Control: no-store`. Scripts, forms, frames and external resources do not run or load. The panel does not try to sanitize the HTML; the header is the boundary.

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

- New tables for pages, maintenance settings and parked domains; one new agent operation (`page.put`, plus `page.delete`); no new `os/exec` exception.
- The nginx templates get a maintenance branch per site (`geo` on the allowlist, `error_page 503` with the page) and a parked-domain template. The container test checks the 503, the allowlisted address and the CSP header with a real nginx.
- Pages cannot run JavaScript. A page that needs a countdown or a status widget does not work and would need a later change of this ADR.
- Images in pages must be small inline `data:` images; 48 KiB caps a page.
- Not in scope: per-page assets, scheduled maintenance windows, maintenance on the panel itself.

## Open questions for Kim

1. Is it fine that pages cannot run JavaScript (strict CSP), in exchange for not having to trust the HTML? Recommendation: yes.
2. Should parked domains get HTTPS with Let's Encrypt, or is HTTP enough for them? Recommendation: HTTPS. Without a certificate, `https://` on a parked domain hits the catch-all server that refuses the TLS handshake [V: `internal/web/nginx.go`], and browsers try HTTPS first more and more often [U].
