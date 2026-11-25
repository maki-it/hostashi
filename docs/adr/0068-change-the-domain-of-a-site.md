# ADR-0068 Change the domain of a site

**Status:** Accepted (Kim, 2026-11-25). **Date drafted:** 2026-11-25. **Decides:** Kim. **Related:** ADR-0005 (nginx), ADR-0018 (API), ADR-0026 (CloudPanel transfer), ADR-0027 (parked domains), ADR-0035 (roles), ADR-0039 (certificate management), ADR-0054 (site types), ADR-0056 (security headers), ADR-0058 (OpenAPI and CLI parity), ADR-0065 (www redirect), ADR-0066 (access rules), [03-features](../03-features.md) "Change the domain of a site" (P1).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- `03-features` lists "Change the domain of a site" as P1: "A site is identified internally by a fixed ID, never by its domain. Vhost, certificate and optionally DB contents are carried along" [V].
- **Identity is already independent of the domain.** A site has an ID and a name the admin types at creation; the name is the Linux user, the home (`<pool>/<name>`), the log directory, the PHP pool, the backup tag and the key of every per-site setting. No table or file is keyed by a domain except `site_domains` and the certificate's names [V: `internal/store/sites.go`, `internal/web/nginx.go` `SiteLogDir/<user>`, `web/src/Sites.tsx`]. Only the CloudPanel scan names a site after its first domain, and the import plan lets the admin change that name [V: `internal/cloudpanel/scan/scan.go`].
- **The domains can already be edited.** `PUT /api/v1/sites/{name}/domains` replaces the list, the first entry is the primary domain; the vhost follows in one web apply and is rolled back if nginx refuses it [V: `hosting.SetDomains`]. The UI has a text area for it on "Domains & HTTPS" [V: `web/src/SitePage.tsx`].
- **The gap is HTTPS.** A site is served over HTTPS only while its live certificate names *every* domain [V: `hosting.certCovers`, `applyWeb`]. After a change with a new name, the whole site falls back to HTTP until the renewal loop has a new certificate [V: comment and `Kick` in `SetDomains`; UI text "The site is served over HTTP until a certificate for the new domains is there"]. That is seconds to a minute when DNS is right [E]. When the DNS of the new name is not ready, the order fails and the next attempt is an hour later (`RetryAfter`) [V], so the site stays on plain HTTP that long. With HSTS on, browsers that know the old name refuse HTTP, so the site is unreachable for them in that time [U: RFC 6797].
- An ACME HTTP-01 order needs every name to answer `/.well-known/acme-challenge/` on port 80 [U]. Today only names in a server block do; unknown host names get `444` from the default server [V: `internal/web/nginx.go` `mainTmpl`]. The challenge files live in one shared directory (`ACMERoot`) that every site block serves for every token [V].
- `TLSInstall` replaces the site's live key and certificate files; nginx keeps using the old ones in memory until the next reload [V: `agent.TLSInstall`; U: nginx loads certificates at reload].
- A removed domain stops answering at once (`444`). After a rebrand or move, the old name usually should send visitors and search engines to the new one with a 301 [U]. Today that needs a second site of type redirect (ADR-0054) with its own certificate, created after the domain is free, so the old name is down in between [V]. ADR-0065 put "redirects between unrelated domains of one site" out of scope [V].
- The CloudPanel import turns redirect hosts that are not the `www` partner into normal aliases and shows "redirect hosts are served as aliases" [V: `internal/cloudpanel/plan/build.go`, ADR-0065 point 8].
- Things that contain the domain but belong to the site's content, not to the panel: WordPress `siteurl`/`home` and URLs in posts (serialized PHP in the database), `APP_URL`-like values in an app's environment (ADR-0054 env), hard-coded URLs in files, external DNS, the site's own mail settings [A: usual cases]. The panel cannot rewrite serialized data safely without a tool such as WP-CLI `search-replace` [U].
- The panel's HTTP server has a 30 s write timeout and nginx a 60 s read timeout for it [V: `internal/serve/serve.go`, `internal/web/nginx.go`]. One certificate may take up to `issueTimeout` (5 min) [V], so it cannot wait inside the request.

## Options

**1. Certificate during the change (Let's Encrypt sites on HTTPS)**

| Option | Pro | Contra |
|--------|-----|--------|
| A. As today: switch at once, HTTP until the certificate is there | No new code | HTTP gap; with HSTS or wrong DNS the site is unreachable or unencrypted for up to an hour |
| **B. Certificate first: the change waits as "pending" until a certificate for the new list is installed, then domains and certificate switch in one web apply** | No HTTP gap; wrong DNS leaves the site as it was and shows the error | A pending state, one migration, ACME challenges for names that are not yet in a server block |
| C. Per-name split: covered names stay on HTTPS, new names are served over HTTP | No pending state | Every site template needs a second full server block for uncovered names; new names still start on HTTP |

**2. What happens to removed domains**

| Option | Pro | Contra |
|--------|-----|--------|
| D. They are free and stop answering (today) | Simple | Old links and search results break |
| **E. "Redirect domains" per site: names that stay on the site and its certificate and answer with 301 to the primary domain** | One step, old name keeps HTTPS, closes the CloudPanel alias gap; reuses ADR-0065's redirect server block | Old names stay on the certificate, so their DNS must keep pointing here, or renewals fail |
| F. After the change, offer to create a redirect site for the old names (prefilled form) | No new site setting | Two steps, a gap for the old name, a second certificate and site |

**3. Content (database, files, app environment)**

| Option | Pro | Contra |
|--------|-----|--------|
| **G. Not rewritten; the dialog lists what the admin has to change and shows the WP-CLI command for WordPress** | No risky writes into site data | Manual step for WordPress |
| H. Rewrite URLs in the site's databases | One click | Serialized PHP, other CMSs, partial matches; needs WP-CLI (not installed) and a backup first; belongs to the WordPress quick install ADR |

## Recommendation

**B, E and G.**

1. **Identity:** the site's name, Linux user, home, logs, databases, backups, cron jobs, Git deploy, settings and grants never change with a domain. Renaming a site is not part of this ADR.
2. **Redirect domains** (option E): a site has its served domains (first = primary, as today) and an optional list of redirect domains. Each redirect domain gets the server blocks of an ADR-0065 redirect (site listeners, certificate, ACME location, site logs, site headers) with `return 301 <scheme>://<primary>$request_uri`. Store: column `site_domains.redirect` (migration 0063). The same uniqueness checks as for served domains apply (other sites, parked domains, the panel address). The certificate names served and redirect domains; an own certificate must cover both (ADR-0039). Served plus redirect domains count against `MaxDomainsPerSite` (50). `www` pairs (ADR-0065) are only formed among served domains. A redirect site (ADR-0054) has no redirect domains. The health check keeps using the first served name.
3. **Certificate first** (option B) when the site wants HTTPS from Let's Encrypt, is live on HTTPS and the live certificate does not cover the new list:
   - The change is stored as **pending** (table `site_domain_changes`, migration 0063: site, domains, redirect domains, who asked, when, last error). The pending names are reserved: another site, a parked domain or a second change cannot take them.
   - The nginx default server for port 80 answers `/.well-known/acme-challenge/` from `ACMERoot` for any host name; everything else stays `444`. The files are already served for any token by every site block, so nothing new becomes readable [V]. A scanner can tell from the 404 that the server is an ACME client [A, accepted].
   - The renewal loop (kicked at once) orders a certificate for the new list. On success it stores the new domains and certificate and applies the web state in one step; the audit entry `site.domains` names the admin who asked. If that apply fails, the old domains come back and the site continues as today after a failed change (HTTP until the next certificate) [A: only on a bug, nginx config is checked before].
   - On failure the change stays pending with the error ("DNS for new.example does not point here", the ACME text), the site stays exactly as it was, and the loop tries again after `RetryAfter`. The admin can **cancel** it, **send it again** (tries at once) or **change now without waiting** (option A, with a warning when HSTS is on).
   - Sites on HTTP, with an own certificate, or whose certificate already covers the new list change at once, as today.
4. **API and CLI** (ADR-0058): `PUT /api/v1/sites/{name}/domains` takes `{"domains": [...], "redirectDomains": [...], "waitForCertificate": true}` (both new fields optional; defaults keep today's clients working). It answers `200` with the site when the change is live and `202` with the site and its `pendingDomains` when it waits. `DELETE /api/v1/sites/{name}/domains/pending` cancels. The site object gets `redirectDomains` and `pendingDomains` (`domains`, `redirectDomains`, `requestedAt`, `error`, `failedAt`). Same action and roles as the domains today (ADR-0035). CLI through the generated client.
5. **UI:** "Domains & HTTPS" keeps the text area for served domains and adds one for redirect domains. A **"Change primary domain"** dialog does the common case in one step: new primary domain, "also `www.<new>`" (preset when the old domain had a `www` pair), "redirect the old domains to the new one" (preset on), a preview of the resulting lists, and the content checklist (option G). A pending change shows as a notice with its state, error and the three buttons. No typed confirmation: the change can be undone.
6. **Content** (option G): nothing in files, databases or app environment is rewritten. The dialog lists what to check: CMS settings (for WordPress the command `wp search-replace 'https://old' 'https://new' --all-tables` with a backup first), app environment values, hard-coded URLs, DNS of the new name, and that the old names' DNS must keep pointing here while they are redirect domains. A rewrite can come with the WordPress quick install ADR.
7. **CloudPanel import:** redirect hosts that are not a `www` partner become redirect domains of the site, and the review note goes away for them.

## Consequences

- A domain change on an HTTPS site never drops to HTTP or breaks HSTS; a DNS mistake shows up as a pending error instead of an outage.
- Old names keep working with a 301 as long as the admin wants; removing them from the redirect list frees them.
- Redirect domains also cover the common "several old domains point to one site" case, so fewer redirect sites are needed.
- One more state for a site (pending domain change) on the site page, in the API and in the renewal loop.
- Port 80's default server answers ACME paths for any host name (404 without a matching token).
- Not in scope: renaming a site or its Linux user, rewriting site content, choosing 302/307/308, a pending state for sites with an own certificate (they must upload a certificate that covers the new list first, as today).
