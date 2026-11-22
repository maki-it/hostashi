# ADR-0059 Real client IP behind CDNs and proxies

**Status:** Proposed. **Date drafted:** 2026-11-22. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0005 (nginx), ADR-0018 (API), ADR-0021 (fail2ban), ADR-0023 (panel HTTPS), ADR-0027 (maintenance pages), ADR-0029 (nginx snippets), ADR-0035 (roles), ADR-0036 (PHP versions workflow), ADR-0042 (panel on port 443), ADR-0053 (site logs), ADR-0054 (site types).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- `03-features.md` lists "Cloudflare origin certificates and real IP handling" as P1, and the v1 check (03-features 3.0) keeps "Cloudflare real IP" in scope [V]. Origin certificates are already covered by own certificates (ADR-0039) [V].
- Kim wants the feature provider-agnostic, not tied to Cloudflare (2026-11-22) [V: project chat]. Some of Kim's customers use Cloudflare, Kim's own sites do not, and Kim wants the setting per site (2026-11-22) [V: project chat].
- `set_real_ip_from`, `real_ip_header` and `real_ip_recursive` are valid in `http`, `server` and `location` [U: nginx docs], so a per-site setting fits into the site's server blocks.
- Behind a CDN or load balancer, nginx sees the proxy's address as `$remote_addr`. Today that address ends up in the site access logs (ADR-0053), in `REMOTE_ADDR` for PHP (`fastcgi_param REMOTE_ADDR $remote_addr`), in `X-Real-IP`/`X-Forwarded-For` for `node`, `app` and `proxy` sites, in the panel's `X-Real-IP` and therefore in the panel's rate limiter, the audit log and the `auth.failed` line that fail2ban reads [V: `internal/web/nginx.go`, `internal/serve/proxy.go`].
- nginx's `ngx_http_realip_module` replaces `$remote_addr` with an address from a header (`real_ip_header`), but only when the connection comes from an address listed in `set_real_ip_from`. With `real_ip_recursive on` and `X-Forwarded-For` it takes the rightmost address that is not trusted, so addresses a client puts in front cannot win [U: nginx docs]. The module is built into the distribution packages of Debian 13 and Ubuntu 26.04 [A: `--with-http_realip_module` in the Debian build; the container test checks it]. The original peer stays in `$realip_remote_addr` [U].
- After the module ran, `allow`/`deny` use the replaced address. That covers the panel allowlist (ADR-0042) and the maintenance allowlist (ADR-0027) [U].
- Cloudflare publishes its ranges at `https://www.cloudflare.com/ips-v4`, `https://www.cloudflare.com/ips-v6` and `https://api.cloudflare.com/client/v4/ips`; on 2026-11-22 these are 15 IPv4 and 7 IPv6 prefixes, and both sources agree [V: fetched]. Neither source is signed [V]. Cloudflare sets `CF-Connecting-IP` to one address and appends the visitor to `X-Forwarded-For` [U].
- Other providers: Fastly, Bunny, AWS CloudFront and most load balancers (Hetzner Load Balancer in HTTP mode, HAProxy, Traefik) send `X-Forwarded-For`; Akamai and Cloudflare Enterprise can send `True-Client-IP` [U]. The PROXY protocol needs `listen ... proxy_protocol`, which then refuses every direct client on that port [U].
- Every site user can run code on the server (PHP, cron, SSH; ADR-0032) and can connect to nginx on `127.0.0.1` with any header [V: no chroot, sites listen on all addresses]. Trusting loopback or the server's own addresses would let one site user forge the address another site or the panel sees.
- fail2ban bans with nftables on the TCP peer. Behind a proxy the TCP peer is the proxy, so a ban of the real visitor does not stop proxied traffic, and a ban of a proxy address would block every visitor who comes through that edge, on all ports for `hostashi-panel` and `recidive` [V: `packaging/fail2ban/hostashi.conf`, `banaction_allports`].
- nginx `limit_req` is not used by Hostashi templates; the panel's own login limiter works on `clientIP` [V].

## Options

**Where the setting lives**

| Option | Pro | Contra |
|--------|-----|--------|
| A. One server-wide setting at the `http` level | One place, covers sites, parked domains, the panel and the database tool alike; trust only applies to connections from the listed ranges, so sites that are not behind the proxy are not affected | A site that is reached directly from a trusted range (rare, for example a Cloudflare Worker that fetches it) gets the header's address |
| **B. A setting per site** | Matches who actually uses a proxy (one customer on Cloudflare, the next not); a site that is not behind a proxy never trusts any header | The panel needs its own setting; forgetting a site leaves proxy addresses in its logs and in fail2ban |
| C. Server-wide ranges, a per-site switch to opt out | Covers the rare case of A | More UI and template paths for a case nobody asked for yet [A] |

**Which header**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. `X-Forwarded-For` with `real_ip_recursive on` by default, header selectable from a fixed list** | Works for Cloudflare and nearly all other proxies, also for a chain (own load balancer behind Cloudflare); spoofed entries on the left are skipped | Needs every hop in the chain to be listed |
| B. Always `CF-Connecting-IP` | Exactly Cloudflare's intent | Cloudflare only, not provider-agnostic |
| C. PROXY protocol | Works for TCP load balancers | Breaks direct clients on the port, not a switch you can try safely |

**Keeping the Cloudflare ranges current**

| Option | Pro | Contra |
|--------|-----|--------|
| A. The server fetches the ranges itself | Always fresh | Unsigned data from the internet changes who may forge client addresses on every server; a network call from the agent |
| **B. The list ships in the code; a weekly workflow (like "PHP versions", ADR-0036) fetches both sources, requires them to agree and opens a pull request** | A human reviews every change, the list is part of a signed release (ADR-0050), servers make no outbound call | New Cloudflare ranges reach servers only with the next release [A: Cloudflare changes its ranges rarely] |

## Recommendation

**B, A and B** (setting per site: decided by Kim, 2026-11-22).

### What is set per site

- Site settings get a card "Real client IP", off by default for every site, also for new and imported sites:
  - **Trusted proxies:** a preset "Cloudflare" (the shipped list, shown read-only with its date) and/or own ranges (CIDR or single address, one per line, at most 256).
  - **Header:** `X-Forwarded-For` (default, recursive), `X-Real-IP`, `CF-Connecting-IP`, `True-Client-IP`. Nothing else, so no header name reaches the config as free text.
- The panel's own host name (ADR-0023, ADR-0042) gets the same card under Settings, admin only, for an admin who puts the panel behind Cloudflare too. Parked domains get no setting (they only show a page).
- The card says plainly: only switch this on if the site is really behind that proxy, and that visitors who reach the server directly still show their own address. Firewall rules for proxy ranges stay a separate step (ADR-0033), not part of this ADR.

### Who may change it

- The Cloudflare preset and the header: everyone with a grant for the site (action `sites.realip:write`, like HSTS and the security headers), because it only changes what this site sees.
- Own ranges: admins only. They also go into fail2ban's server-wide `ignoreip` (below), so a wrong range would make addresses unbannable for the whole server, the panel login included.

### Checks (panel and again in the agent)

- Every range is parsed with `net/netip` and rendered from the parsed value.
- Refused: `0.0.0.0/0`, `::/0`, IPv4 prefixes shorter than `/8`, IPv6 shorter than `/16` [A: bounds wide enough for Cloudflare's `/13` and `/29`]; loopback, link-local, unspecified and multicast; any address of the server's own interfaces, private ones included (the agent reads them like `internal/netaddrs`). Private ranges (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`) are allowed, for an own load balancer in a private network.
- A refusal is `422 real_ip_rejected` with the range and the reason.

### nginx rendering

- In every server block of a site that has the setting on (HTTP and HTTPS, redirect and maintenance included): one `set_real_ip_from` per range, `real_ip_header <header>;`, and `real_ip_recursive on;` for `X-Forwarded-For`. The `http` level and all other sites get nothing. The maintenance allowlist (ADR-0027) then checks the real address.
- The panel's server blocks get the panel's own setting the same way.
- The site access logs (ADR-0053, today nginx's default `combined` format) get a Hostashi `log_format` that is `combined` plus the original peer as an extra field at the end (`$realip_remote_addr`), so an admin can still see which edge a request came through; existing log readers in the panel ignore extra fields [A, checked in the site logs tests].
- PHP gets the real address through the existing `REMOTE_ADDR $remote_addr`; `node`, `app` and `proxy` sites get it in `X-Real-IP` and as the last entry of `X-Forwarded-For`, as today. Without its own setting the panel sees the proxy's address. The panel server keeps sending `X-Real-IP $remote_addr` and clearing `X-Forwarded-For`, so the panel sees the real address without trusting any header itself.
- Snippets (ADR-0029) keep refusing `set_real_ip_from` and `real_ip_header` [V: not on the allowlist today; a test pins it].

### fail2ban

- `ignoreip` of the Hostashi jail file gets the Cloudflare list as soon as one site or the panel uses the preset, plus the own ranges that admins set, so fail2ban never bans a proxy edge. fail2ban bans per address on all ports, so this cannot be per site.
- Bans of real visitors still go into nftables but do not stop traffic that comes through the proxy. The panel's own limiter (on the real address) keeps protecting the login. The card says so, and points to the proxy's own tools (for example Cloudflare's WAF rules) for blocking there.

### Storage, API, agent

- Storage: migration 0053 with one table `site_realip` (one row per site: `cloudflare`, `ranges` as JSON checked by `json_valid`, `header`); no row means off. The panel's own setting is a settings key `panel.realip` with the same JSON shape.
- API: `GET` and `PUT /api/v1/sites/{name}/real-ip` (action `sites.realip:write`, own ranges need the admin role, `403` otherwise) and `GET`/`PUT /api/v1/settings/panel-real-ip` (admin only), new routes with OpenAPI records (ADR-0058). No new token scope: `sites:write` and `sites:read` cover the site routes. Every change goes into the audit log with old and new values.
- Agent: `web.Site` and `web.Panel` get a typed `RealIP` field that `web.apply` checks and renders; the fail2ban jail file is written by the existing fail2ban package with the new `ignoreip` values. No new agent operation, no new `os/exec` exception, `nginx -t` with rollback as today.
- The Cloudflare list lives in Go (`internal/agentapi`, with the date it was fetched). A weekly workflow `Proxy ranges` fetches the three Cloudflare URLs, fails if they disagree, refuses a list that breaks the checks above, and opens or updates a pull request; nothing is merged automatically. Other providers can get presets later the same way.

## Consequences

- Sites with the setting on, their PHP, apps and logs see the visitor's address behind Cloudflare or any configured proxy; other sites are not touched. The panel's audit log and login limiter see it once the panel's own setting is on.
- A wrong range lets whoever sends from it choose the client address the server believes. That is the main risk; the checks above, own ranges for admins only and the audit log limit it, and loopback and own addresses can never be trusted.
- fail2ban does not ban proxy edges any more, but also cannot block visitors who come through a proxy. That is a property of the network path, not of this setting.
- Not in v1: PROXY protocol, presets for other providers, automatic firewall rules for the proxy ranges.
- Tests: unit tests for the range checks (too wide, loopback, own address, IPv4-mapped IPv6, duplicates, garbage), template tests with and without the setting and for each header; a container test with real nginx that sends a request from a trusted and from an untrusted address with a forged `X-Forwarded-For` and checks the access log, `REMOTE_ADDR` in PHP and the panel's audit entry.
- Work estimate: 5 small PRs (agent and nginx, fail2ban, store and API, panel cards with screenshots first, wiki) plus the workflow [E].

## Open questions for Kim

None left.

1. ~~Should the setting be server-wide or per site?~~ **Decided (Kim, 2026-11-22): per site.**
