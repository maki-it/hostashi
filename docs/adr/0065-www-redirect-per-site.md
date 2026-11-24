# ADR-0065 www/non-www redirect per site

**Status:** Accepted (Kim, 2026-11-24). **Date drafted:** 2026-11-24. **Decides:** Kim. **Related:** ADR-0005 (nginx), ADR-0017 (import and presets), ADR-0018 (API), ADR-0026 (CloudPanel transfer), ADR-0039 (certificate management), ADR-0054 (site types), ADR-0056 (security headers), ADR-0058 (OpenAPI and CLI parity).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- "www/non-www redirect as a setting" is a P0 row of the 1.0 scope that has no code yet [V: `docs/03-features.md` 3.0].
- A site has a list of domains, the first one is the primary domain. All of them go into one `server_name` and serve the same content; there is no redirect between them [V: `internal/web/nginx.go`, `siteTmpl`].
- The Let's Encrypt certificate of a site must name every domain of the site. [V: `internal/hosting/tls.go` `certCovers`, `agentapi.MissingNames`]. One name that fails HTTP-01 (for example `www` without a DNS record) fails the whole order [U: ACME orders are all-or-nothing]. An own certificate (ADR-0039) must also cover every domain [V: `checkOwnCertDomains`].
- The agent checks a reload with a request for `/.hostashi/health` with the primary domain as host name, through the control socket [V: `Nginx.Verify`].
- CloudPanel vhosts often have a second `server` block for `www.<domain>` that returns 301 to the apex. The import finds these names (`RedirectFrom`), adds them as normal domains and tells the admin "redirect hosts are served as aliases until typed redirects exist" [V: `internal/cloudpanel/scan/scan.go`, `internal/cloudpanel/plan/build.go`].
- Search engines treat `example.com` and `www.example.com` as two sites with the same content unless one redirects to the other; a 301 is the usual signal [U].
- 301 lets a browser turn a POST into a GET, 308 keeps the method [U: RFC 9110]. Visitors who reach the wrong name almost always send a GET [A].
- The redirect site type (ADR-0054) already sends every name to one target [V].

## Options

**Where the partner name comes from**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Both names are normal domains of the site; the setting only decides which of each pair redirects** | Domain checks (other sites, parked domains, reserved names), the certificate and DNS stay as they are; nothing appears that the admin did not type | The admin adds `www.example.com` by hand; the panel offers a button for it |
| B. The setting adds the missing partner name implicitly | One click | A partner without DNS fails the certificate of the whole site; the hidden name can collide with another site or a parked domain, and every domain check needs a second code path |

**How nginx redirects**

| Option | Pro | Contra |
|--------|-----|--------|
| **C. A second `server` block per site for the names that redirect, with the same listeners and certificate, the ACME location and `return 301`** | No `if` in the site's server; snippets, maintenance and caches never see the redirected names | One more block per site with the setting on |
| D. `if ($host = ...) { return 301 ...; }` in the site's server | One block | Mixes redirect and site logic; every location of the site is in its scope [U: nginx "if is evil" only bites outside `return`, but the template gets harder to read] |

## Recommendation

**Option A with C.** Decided by Kim on 2026-11-24: both names are listed explicitly, the panel offers a button to add the missing one.

1. **Setting `wwwRedirect` per site** with three values: `off` (default, today's behaviour), `to-www` (`example.com` → `www.example.com`) and `to-non-www` (`www.example.com` → `example.com`). Store column `sites.www_redirect` (migration 0060), empty for `off`.
2. **Pairs:** a domain `d` that does not start with `www.` and `www.d` are a pair when both are domains of the site. With `to-www` the name `d` redirects, with `to-non-www` the name `www.d`. Domains without a partner are served as today. `www.www.example.com` is never part of a pair, so there are no chains. A site may have several pairs (`example.com`, `example.de` and their `www` names).
3. **Turning it on** needs at least one pair: otherwise the panel answers 400 and names the missing domain (`add www.example.com as a domain first`). The UI shows this as a button that adds the partner through the existing domain change. If a later domain change removes every pair, the setting stays and the site page says it has no effect.
4. **nginx:** the redirect names get their own `server` blocks (HTTP and, with a certificate, HTTPS) with the site's certificate, the ACME challenge location, the site's log files, the site's headers (ADR-0056, so HSTS is also sent for the redirected name) and `return 301 <scheme>://<partner>$request_uri`. The scheme is `https` when the site is on HTTPS, else `http`. Snippets, maintenance, disabled pages and caches are only in the site's own server. The health check uses the first domain that is not redirected.
5. **Certificate:** unchanged, it still names every domain, so both names of a pair are covered (option A).
6. **Site types:** every type except `redirect`, which already sends every name to its target; the setting is refused there. A site that becomes a redirect keeps the stored value without using it, so a change back restores it (as the redirect target in ADR-0054 addendum 3).
7. **API and CLI:** `PUT /api/v1/sites/{name}/www-redirect` with `{"mode": "off" | "to-www" | "to-non-www"}`, `wwwRedirect` in the site object, OpenAPI record (ADR-0058), scope as the other site web settings, audit entry `site.www_redirect` with old and new value. Same roles as for the site's domains (ADR-0035).
8. **CloudPanel import:** a `RedirectFrom` name that is the `www` partner of the site's primary domain sets `to-non-www`, the reverse case `to-www`; the review note goes away for these. Other redirect hosts stay aliases with the note.
9. **Status 301**, fixed. No 302/307/308 choice in v1.

## Consequences

- Closes the P0 row and the CloudPanel import gap for the common `www` case.
- The admin decides which names are on the certificate; a missing DNS record for `www` stays visible as a certificate error and does not appear out of nowhere.
- Turning the setting on or off is a normal web reload, no certificate order.
- Not in scope: redirects between unrelated domains of one site (use a redirect site or a parked domain), other status codes, `www` handling for parked domains.
