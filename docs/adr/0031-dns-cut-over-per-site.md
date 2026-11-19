# ADR-0031 DNS cut-over per site in the CloudPanel migration

**Status:** Proposed. **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0017 (CloudPanel import as migration), ADR-0026 (data transfer), ADR-0030 (loading dumps), ADR-0023 (panel HTTPS), [`03-features.md`](../03-features.md) (DNS-01 and DNS record management).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- The importer creates each site on the new server without a certificate, because the domains still point to the old server [V: `siteCreator` in `internal/serve/migrations_api.go`]. Certificates come from Let's Encrypt with HTTP-01 [V: ADR-0023, `internal/acme`], which only works once a domain resolves to the new server [U].
- Files and databases are copied while the old site is live; a final run copies only what changed, the dumps are loaded as the owner account and the import is activated in one rename [V: ADR-0026 Part 3, ADR-0030, PR 136].
- The old server stays untouched until DNS is switched, and switching back is the rollback [V: ADR-0017].
- Anything written on the old site between the final dump and the moment visitors reach the new server is lost on the new one (orders, comments, form entries) [A].
- Hostashi manages no DNS records today. DNS-01 with INWX, Cloudflare and Hetzner DNS is P1, record management through a provider API is P2 [V: `03-features.md`]. Where the DNS zones of the 22 sites on web2 live is not known [U]; Kim's own domains are at INWX [A].
- Resolvers cache a record for its TTL; a TTL of one day means visitors keep reaching the old server for up to a day [U].

## Part 1: who changes the records

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Kim changes the records at the DNS provider; the panel shows the records to set, watches the domains and issues the certificate as soon as they point here** | Works for every provider, including customers' zones Hostashi has no access to. No credentials for a DNS account in Hostashi. Nothing to build for a provider | One manual step per site in the provider's interface |
| B. Hostashi changes the records itself through the INWX API | One click per site | A credential that can change every zone of the account sits in the panel. Only INWX; other providers need their own code. Pulls the P2 record management into the importer |
| C. A, and B later for providers that are connected for DNS-01 | Starts simple, keeps the door open | Two paths to test once B exists |

**Recommendation: C, building A now.** The switch is a one-time step per site, and a key that changes zones is a bigger risk than the few minutes saved [A]. Once DNS-01 brings a provider integration, the cut-over can use it as an option.

## Part 2: the order per site

1. **Days before:** Kim lowers the TTL of the site's records to 300 seconds. The panel shows the TTL it sees for each domain and warns above 300 seconds [A].
2. **Test before the switch:** the site can be opened on the new server with an entry in the local hosts file, over HTTP only, because there is no certificate yet. Sites that force HTTPS cannot be tested this way [A]; a test host name below the panel's domain is possible later.
3. **Final sync:** a new import run copies the changes and the dumps, the panel loads them, and the import is activated [V: existing API].
4. **Switch:** Kim sets the `A` and `AAAA` records the panel shows. The panel takes the server's public addresses from its network interfaces (global unicast only); behind NAT the admin sets them once in the settings [A].
5. **Watch:** the panel resolves every domain of the site once a minute for up to 48 hours. As soon as all `A` and `AAAA` answers name only this server's addresses, it marks the site as switched and turns on HTTPS, which issues the certificate through the existing renewal loop. HSTS stays as the admin chose it [A]. A domain that still points elsewhere is shown with what it resolves to.
6. **Rollback:** setting the records back to the old server; the old server was never changed [V: ADR-0026].
7. **After the last site:** the panel reminds Kim to remove the `authorized_keys` line and the dump user on the old server and closes the migration source [V: ADR-0026 Consequences].

The watch uses the system resolver of the new server. A caching resolver there may answer from its cache for up to the old TTL, which only delays the certificate [A]. Asking the zone's authoritative servers directly would need a DNS library and is left out [A].

## Part 3: writes during the switch

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Per site, chosen in the review: either accept the short gap, or Kim stops writes on the old site by hand right before the final sync (for example the shop's own maintenance mode)** | No change to the old server by Hostashi, which only reads it. The gap is minutes with a low TTL [E] | A manual step for sites that write |
| B. The old server forwards all requests to the new one after the final sync | No lost writes, no gap | Needs a change in CloudPanel's nginx on the old server, which the read-only key cannot and should not do |
| C. Copy again after the switch and merge | | Merging two diverged databases is not safe to automate [A] |

**Recommendation: A.** The review shows which sites look like they write (shop plugins, form plugins, a large `wp_comments`) only as a hint, without blocking [A].

## Consequences

- New per-site state in the panel database: the cut-over (waiting for DNS, switched, failed after 48 hours) with the last answer per domain. It survives a panel restart, unlike the in-memory load watch of ADR-0030 [A].
- New API: `GET` and `POST /api/v1/migrations/cloudpanel/sites/{name}/cutover` (scopes `migrations:read` and `migrations:write`) to start, read and cancel the watch [A].
- No new dependency and no new `os/exec` exception: the watch uses Go's resolver [A].
- No credential for a DNS account in Hostashi [A].

## Open questions

1. Does Kim change the records by hand with the panel watching (A now, C later), or should Hostashi change them through INWX right away (B)?
2. Are the zones of all 22 sites at INWX, or are some at customers' providers? This only changes how much option B would cover.
