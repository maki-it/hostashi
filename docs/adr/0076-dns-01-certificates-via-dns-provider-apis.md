# ADR-0076 DNS-01 certificates via DNS provider APIs

**Status:** Proposed. **Date drafted:** 2026-11-26. **Decides:** Kim. **Related:** ADR-0003 (stack, lego), ADR-0004 (web/agent privilege split), ADR-0023 (panel address), ADR-0031 (DNS check), ADR-0035 (roles), ADR-0039 (own certificates), ADR-0058 (OpenAPI and CLI), ADR-0068 (change the domain of a site), ADR-0075 (wildcard domains).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants Let's Encrypt certificates for wildcard domains (ADR-0075) and decided that DNS-01 comes before ADR-0075 is built (2026-11-26).
- Certificates are issued over HTTP-01 only. lego v5 runs as a library in the unprivileged web process; the agent keeps every private key and hands in a CSR [V: `internal/acme/acme.go`, `go.mod` lego v5.7.0].
- A CA issues wildcard names only over DNS-01: the client publishes a TXT record `_acme-challenge.<name>` in the domain's zone [U].
- lego v5.7.0 ships DNS providers for INWX, Hetzner, Cloudflare and many more, plus generic ones (`acmedns`, `httpreq`, `exec`) [V: `providers/dns/` in the module]. The Hetzner provider uses the DNS API of the Hetzner Cloud API [V: `providers/dns/hetzner/hetzner.toml`]. lego follows a CNAME on `_acme-challenge.<name>` and writes the TXT record at its target [V: `challenge/dns01/client_cname.go`].
- Kim's domains are at INWX; some customers use Cloudflare; Kim's servers are at Hetzner [V: project memory].
- Hetzner Cloud API tokens are per project and either read-only or read/write for the whole project, servers included [U]. INWX API logins have the rights of the account or sub-user they belong to [U]. A DNS token in the panel is therefore often much more powerful than "write one TXT record".
- Scope v1 excludes a DNS server. Calling a provider's API as a client is not a DNS server, but it is a new kind of outside dependency and a new kind of secret in the panel [A: Kim's reading of the scope rule decides].
- Secrets are already stored sealed with the panel's secret key in the settings table, for example the ACME account key [V: `internal/acme/acme.go` KeyStore].
- `os/exec` is forbidden in the web process by depguard [V: `.golangci.yml`].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. lego's built-in DNS providers, a short allowlist (INWX, Hetzner, Cloudflare), credentials per "DNS account" in the panel** | No new library (lego is already there); every provider is a Go package, no exec; renewals stay automatic | Every provider adds its own dependencies (license check); powerful tokens stored in the panel |
| B. Only CNAME delegation to an acme-dns server (`acmedns` provider) | The token can only change TXT records in a throwaway zone | Needs an acme-dns server somewhere, which is a DNS server (out of scope) or a third-party service |
| C. lego `exec` or `httpreq` provider, so the owner hooks up any script or webhook | Works with every provider | `exec` breaks the no-exec rule; `httpreq` needs a service the owner runs; hard to support |
| D. No DNS-01; wildcards only with uploaded certificates (ADR-0039) | Nothing new | No automatic renewal; ADR-0075 stays manual, which Kim does not want |

## Recommendation

**Option A, with CNAME delegation as the recommended setup in the docs.** B and C are not recommended. D is today's state.

**Where it runs**

- DNS-01 runs in the web process next to HTTP-01, through the same lego client. The agent is not touched apart from accepting `*.` names in the CSR for ACME (see ADR-0075). No new `os/exec`, no root.
- Outbound HTTPS to the provider's API and DNS queries on port 53 to the authoritative name servers (lego's propagation check) come from the server [A: the host firewall allows outbound traffic].

**Providers in the first version**

- INWX (`inwx`), Hetzner (`hetzner`, Cloud API) and Cloudflare (`cloudflare`). Each provider is imported on its own, never `providers/dns` as a whole, so only their dependencies come in. Each dependency must pass the license allowlist and the latest version is checked first [A: not yet checked for these three].
- More providers are added one at a time on request, each a small PR with its own license check.
- `exec`, `httpreq` and `acmedns` are not offered.

**DNS accounts**

- New table `dns_accounts` (migration number from the coordinator when it is built): id, label, provider, sealed credentials (JSON of the provider's fields), zones (list of zone names this account may serve), created/updated.
- Credentials are sealed with the panel's secret key, never returned by the API (write-only fields; the UI shows "set" and a "replace" action), never logged, never in backups of the panel database in clear text [V for sealing as a pattern; A that backups only carry the sealed value].
- "Check access" in the UI asks the provider for the zone (or creates and deletes a test TXT record where there is no read call) and shows the result.
- Only admins manage DNS accounts (ADR-0035). Every change is in the audit log, without secret values.

**Which names use DNS-01**

- A per-site setting "Certificate check: HTTP (default) / DNS". For DNS, each domain of the site must fall into a zone of a DNS account (longest matching zone wins). A site with a `*.` name (ADR-0075) needs DNS.
- The panel address (ADR-0023) can use DNS too; then port 80 need not be open for it.
- Side benefit: a site can get its certificate before its DNS points to this server, for imports and the domain change of ADR-0068 [A: helpful, no extra work].

**CNAME delegation (recommended in the docs)**

- The owner creates `_acme-challenge.example.com CNAME example.com.<acme-zone>` once, where `<acme-zone>` is a small zone used only for this. The DNS account then only needs rights on that zone. lego follows the CNAME by itself.
- For Hetzner the docs say plainly: create a separate Hetzner Cloud project that holds only the DNS zones, and use its token. A token of a project with servers can delete those servers [U for the token scope].
- For Cloudflare: an API token limited to "Zone: DNS: Edit" on the needed zones, never the global API key.
- For INWX: a sub-user with DNS rights only, if the account offers it [U].

**Renewal and errors**

- Renewal uses the existing loop; nothing changes for HTTP-01 sites.
- A failed DNS-01 order shows the provider's error (without secrets) on the site, sends the existing certificate warning mail and keeps the old certificate.
- Let's Encrypt rate limits apply as today.

**API, CLI and UI**

- `GET/POST /api/v1/dns-accounts`, `GET/PUT/DELETE /api/v1/dns-accounts/{id}`, `POST /api/v1/dns-accounts/{id}/check`. Deleting an account that a site uses is refused with the list of sites.
- The site's TLS settings get `challenge: "http" | "dns"`.
- New routes with `mux.Op` and OpenAPI entries (ADR-0058); CLI commands `hostashi dns-account …`.
- UI: a page "DNS accounts" under Settings, the provider's fields as a form, and the choice on the site's "Domains & HTTPS" tab.

## Consequences

- New outside dependencies: the three lego provider packages and their libraries (for example `github.com/nrdcg/goinwx` for INWX [V: listed in lego's `go.mod`]). They are updated with lego.
- New kind of secret in the panel: DNS API credentials, often with more power than needed. Mitigations: sealed storage, write-only API, admin only, audit, and docs that push CNAME delegation and narrow tokens. If the panel database and the secret key are both stolen, these credentials are too; that risk is the same as for the SMTP password today [A].
- The server talks to provider APIs. If a provider changes or removes an API, DNS-01 for that provider breaks until lego is updated.
- Unlocks ADR-0075 with automatic Let's Encrypt wildcard certificates.
- Effort [E]: 1 PR backend (table, sealing, providers, lego DNS-01, renewal, API, OpenAPI, CLI, tests with lego's provider test servers), 1 PR UI and wiki with screenshots. ADR-0075 then builds on it.
- Tests: zone matching (longest zone, no match, wildcard), sealed round trip, secrets never in API responses or logs, delete refused while in use, permission test, challenge type switch, failed order keeps the old certificate, provider calls against a fake HTTP server.

## Open questions for Kim

1. ~~Providers in the first version: INWX, Hetzner and Cloudflare?~~ Decided (Kim, 2026-11-26): yes, these three.
2. ~~Build for 1.0 or 1.1?~~ Decided (Kim, 2026-11-26): 1.1, before ADR-0075.
