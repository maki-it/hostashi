# ADR-0075 Wildcard domains for reverse proxy and redirect sites

**Status:** Proposed. **Date drafted:** 2026-11-26. **Decides:** Kim. **Related:** ADR-0005 (nginx), ADR-0023 (panel address), ADR-0031 (DNS check), ADR-0039 (own certificates), ADR-0042 (panel on 443), ADR-0054 (site types), ADR-0058 (OpenAPI and CLI), ADR-0065 (www redirect), ADR-0068 (change the domain of a site), ADR-0076 (DNS-01 certificates).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim asked whether a reverse proxy or redirect site can take a wildcard domain, so that every name that no other site serves goes there (2026-11-26).
- Today a site domain must be an exact host name. `agentapi.ValidateDomain` refuses `*` and is used for site domains, parked domains, the panel address and certificates [V: `internal/agentapi/web.go`, `internal/hosting/`].
- Unknown host names reach the sites' default server: on port 80 it answers ACME challenges and otherwise closes the connection (444), on 443 it refuses the TLS handshake [V: `internal/web/nginx.go`, ADR-0068]. No site can take that place.
- Certificates from Let's Encrypt are issued over HTTP-01 only [V: `internal/acme/acme.go`]. A CA does not issue wildcard certificates over HTTP-01; it needs DNS-01 [U]. Hostashi runs no DNS server and has no DNS provider integration (v1 scope).
- Uploaded certificates (ADR-0039) may already contain wildcard names, and `agentapi.CertCovers` matches `*.example.com` against exactly one more label [V: `internal/agentapi/tls.go`, `internal/certs/custom.go`].
- nginx picks a server block by name in this order: exact name, longest leading wildcard (`*.example.com`), longest trailing wildcard, first matching regex, then the default server [U]. A leading wildcard matches names of any depth (`a.b.example.com` too), and it does not match `example.com` itself [U].
- `site_domains.domain` is the primary key, so one name, including a future `*.example.com`, belongs to at most one site [V: migration 0006].
- The CloudPanel scan reports wildcard and regex `server_name` entries as "not modeled" [V: `internal/cloudpanel/scan/scan.go`].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Named wildcard `*.<domain>` as a domain of a proxy or redirect site** | Uses nginx's own name matching: exact names of other sites, parked domains and the panel always win; the effect stays inside a domain the owner controls; HTTPS works with an uploaded wildcard certificate | No automatic certificate without DNS-01; uploaded certificates must be renewed by hand |
| B. Server-wide catch-all: one site becomes the default server for every unknown name | Catches every name, even foreign domains | On 443 there is no valid certificate for arbitrary names, so visitors see a certificate error; scanners, IP-only requests and stale DNS records of third parties all become traffic of that site; replaces the safe 444 / handshake refusal |
| C. A plus DNS-01 through a DNS provider API (for example INWX or Hetzner DNS) | Automatic wildcard certificates and renewal | New dependency per provider, API credentials stored in the panel, a provider abstraction to maintain; not a DNS server, but clearly bigger than this ADR |

Kim chose A on 2026-11-26 (decision card in the project thread).

## Recommendation

**Option A, built after ADR-0076.** B is not recommended: it weakens the default server for little gain. C is split out into ADR-0076: Kim decided on 2026-11-26 that DNS-01 comes first, so wildcards get Let's Encrypt certificates from the start.

**Which names**

- A new `agentapi.ValidateDomainPattern` accepts either an exact domain (`ValidateDomain`) or `*.` followed by an exact domain with at least two labels. `*.com`, `*`, `a.*.example.com`, `*example.com` and `**.example.com` are refused. Public suffixes such as `*.co.uk` are not checked [A: there is no public suffix list in the code, and the server has a single owner; the name only routes traffic that already reaches this server].
- `ValidateDomain` itself stays strict. Only the domain list of sites of type proxy and redirect uses the pattern check. Parked domains, the panel address, redirect domains of ADR-0068 and every other site type keep exact names.
- The primary domain (position 0) must be an exact name. The health check, the www logic and the UI's "open site" link use it [V: ADR-0065 uses the first served name].
- `*.example.com` does not cover `example.com`. Both can be listed on the same site.
- The wildcard counts as one domain towards `MaxDomainsPerSite`.

**Conflicts**

- The literal name stays unique across all sites (primary key).
- Exact names on other sites, parked domains and the panel address may sit below a wildcard of another site. nginx sends them to their own block. The UI shows a note on the wildcard site that lists these names ("served elsewhere") [A: helpful, not required].
- `*.shop.example.com` and `*.example.com` may coexist. nginx uses the longer match [U].

**Redirect and proxy behaviour**

- Redirect sites: the self-redirect check compares the target host with `CertCovers(ownDomains, host)`, so a target such as `https://www.example.com` is refused when the site has `*.example.com`. The target stays fixed. A redirect to "the same subdomain on another domain" (`$host`-based) is out of scope.
- Proxy sites already pass `Host $host` to the backend [V: `internal/web/nginx.go`], so a multi-tenant backend sees the real subdomain. A fixed `Host` override stays possible as today.
- Changing the site type away from proxy or redirect (ADR-0054 addendum 3) is refused while a wildcard is in the domain list, with a message that names it.

**TLS**

- A site with a wildcard domain gets HTTPS in one of two ways: Let's Encrypt over DNS-01 (ADR-0076), with every domain of the site in a zone of a DNS account, or an uploaded certificate (ADR-0039) that covers every domain. `CertCovers` already handles the second.
- Let's Encrypt over HTTP-01 is refused while the domain list contains a wildcard. The error names the wildcard and points to "DNS accounts" and "Upload certificate". HTTP-only stays allowed.
- `TLSCSRRequest.Validate` refuses `*.` names today [V: `internal/agentapi/tls.go`]. It switches to the pattern check, for ACME and for custom CSRs.
- Uploaded wildcard certificates are covered by the existing expiry warnings (overview 30/7 days, doctor, `/metrics`), and the site page says that they are not renewed automatically.
- Deeper names (`a.b.example.com`) match the nginx block but are not covered by a one-level wildcard certificate. They get a certificate error over HTTPS. The UI says so next to the field [U for the nginx behaviour].

**DNS check (ADR-0031)**

- For `*.example.com` the check resolves a random label (`hostashi-check-<random>.example.com`) and reports whether a wildcard record points to this server [A].

**API, CLI and UI**

- No new route. `PUT /api/v1/sites/{name}/domains` and create site accept `*.` names for proxy and redirect sites. The OpenAPI description of the domain field states the rule (ADR-0058). The CLI needs no change besides help text.
- The domain field on proxy and redirect sites accepts `*.example.com` with a hint: "Matches every subdomain that no other site serves. HTTPS needs an uploaded wildcard certificate."
- The audit log records the change as any other domain change.

**CloudPanel import**

- Stays as today for this ADR (review note). A later small PR may map `*.x` on imported proxy sites [A].

## Consequences

- No new `os/exec` exception and no new agent operation. The agent's site config gets a validated pattern instead of an exact name, and the nginx template writes it into `server_name` as it is. The pattern allows only `*`, `.`, letters, digits and `-`, so no nginx syntax can be injected [V for the allowed characters, by construction].
- The default server keeps refusing unknown names. The wildcard only widens what a single site answers within its own domain.
- Depends on ADR-0076 for automatic certificates. With an uploaded certificate the owner renews by hand; forgetting it breaks HTTPS for every covered subdomain at once, and the existing expiry warnings are the safeguard.
- Changes: `agentapi` pattern check and tests, site domain validation per type, the redirect self-target check, the Let's Encrypt source guard, the type-change guard, DNS check, OpenAPI text, UI hint, wiki page "Site types" and "Domains & HTTPS".
- Effort [E]: 1 PR backend (validation, nginx, guards, OpenAPI, tests), 1 PR UI and wiki with screenshots.
- Tests: pattern accept and reject table (`*.com`, `*`, `a.*.b.c`, upper case, trailing dot, IDN in xn-- form, length limits), primary domain not a wildcard, uniqueness, wildcard refused on php/static/node/python/app sites and on parked domains, nginx output with an exact name on another site, self-redirect through the wildcard, Let's Encrypt refused, uploaded wildcard certificate accepted, type-change guard, DNS check with a random label.

## Open questions for Kim

1. ~~Named wildcard per site (A), server-wide catch-all (B) or both?~~ Decided (Kim, 2026-11-26): A.
2. ~~Proxy and redirect sites only, or also PHP and static sites?~~ Decided (Kim, 2026-11-26): proxy and redirect only for now.
3. Build for 1.0, or after the v1 release (1.1)? Recommendation: 1.1, right after ADR-0076, unless a site of yours needs it now.
