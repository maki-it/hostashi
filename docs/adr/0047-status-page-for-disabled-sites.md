# ADR-0047 Status page for disabled sites

**Status:** Proposed. **Date drafted:** 2026-11-21. **Decides:** Kim. **Related:** ADR-0027 (maintenance and parking pages), ADR-0018 (API), ADR-0035 (users and roles), PR #93 (disable a site).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- A disabled site answers every request with a fixed 503 HTML string that is written into the nginx template; PHP stops, ACME challenges, the health check and the HTTPS redirect stay [V: `internal/web/nginx.go`, `internal/hosting/hosting.go`].
- That page is English only, unstyled and the same for every site. It cannot say why the site is off or whom to contact [V: template].
- ADR-0027 already has stored pages (single HTML or ZIP, checked, sent to the agent in chunks, served under `/.hostashi/page/<id>/` with a strict CSP) of the kinds `maintenance` and `parking`, each with a built-in page [V: `internal/statuspage`, `internal/hosting/pages.go`].
- Kim asked for a status page when a site is disabled, as a v1 feature after the redesign (2026-11-20).
- Typical reasons to disable a site at Maki IT: the customer stopped paying, the contract ended, the site was compromised [A]. Each wants a different text than "maintenance".

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| A. Keep the fixed string, only make it nicer (styled, German and English) | Smallest change | Still one text for every site and reason |
| **B. A disabled site shows a stored page of kind `maintenance`** | Reuses ADR-0027 as is, no new kind, one list of pages | The built-in maintenance text ("back soon") does not fit every reason; an own page per reason is one upload away |
| C. A third page kind `disabled` with its own built-in page | Clear lists per purpose | One more kind and one more built-in page for little gain |

## Recommendation

**Option B** (decided by Kim, 2026-11-21: reuse the maintenance pages instead of a kind of its own, which the draft recommended).

- Disabling a site takes an optional page of kind `maintenance`; empty means the built-in maintenance page. The page can be changed while the site stays disabled. A page that a disabled site uses counts as in use and cannot be deleted, as in ADR-0027.
- nginx serves the page as for maintenance: its files under `/.hostashi/page/<id>/`, every other URL gets `index.html` with status **503** and the same CSP, `X-Content-Type-Options` and `Cache-Control: no-store`, but **without** `Retry-After`, because nobody knows when the site comes back. There is no allowlist: disabled means off for everyone. PHP stays stopped, snippets stay out, ACME and the health check are unchanged.
- Disabled still wins over maintenance. The page of a disabled site is stored apart from the maintenance settings, which are kept and apply again after the site is enabled.
- API: `PUT /api/v1/sites/{name}/disabled {"disabled": true, "page": "<id>"}`, `page` optional. Same scope as today (`sites.disabled:write`). Pages use the existing page API and scopes (`pages:read`, `pages:write`).
- The fixed string in the template goes away. The panel sends the page to the agent before `web.apply` if the agent lacks it, the same path as for maintenance [V: `ensurePage` logic in `internal/hosting/pages.go`].

## Consequences

- No new table: one column for the page of a disabled site. No new agent operation and no new `os/exec` exception; `web.Site` gets a page reference for the disabled branch.
- Tests: nginx unit tests for the disabled branch (page, 503, no `Retry-After`, CSP), the container test with a real nginx, API tests for a wrong kind, an unknown page and deleting a page in use.
- The same script risk as in ADR-0027 applies to uploaded maintenance pages shown on a disabled site (named conflict there, accepted by Kim on 2026-11-19). The built-in page has no script.
- Not in scope: a reason field shown on the page, scheduled disabling, a page per domain of a site.

## Open questions for Kim

None left.

1. ~~Own page kind `disabled` or reuse the maintenance pages?~~ Decided (Kim, 2026-11-21): reuse the maintenance pages.
