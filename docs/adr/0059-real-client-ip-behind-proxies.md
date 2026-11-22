# ADR-0059 Real client IP behind CDNs and proxies

**Status:** Proposed. **Date drafted:** 2026-11-22. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0005 (nginx), ADR-0018 (API), ADR-0021 (fail2ban), ADR-0023 (panel HTTPS), ADR-0027 (maintenance pages), ADR-0029 (nginx snippets), ADR-0035 (roles), ADR-0036 (PHP versions workflow), ADR-0042 (panel on port 443), ADR-0053 (site logs), ADR-0054 (site types).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- `03-features.md` lists "Cloudflare origin certificates and real IP handling" as P1, and the v1 check (03-features 3.0) keeps "Cloudflare real IP" in scope [V]. Origin certificates are already covered by own certificates (ADR-0039) [V].
- Kim wants the feature provider-agnostic, not tied to Cloudflare (2026-11-22) [V: project chat].
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
| **A. One server-wide setting at the `http` level** | One place, covers sites, parked domains, the panel and the database tool alike; trust only applies to connections from the listed ranges, so sites that are not behind the proxy are not affected | A site that is reached directly from a trusted range (rare, for example a Cloudflare Worker that fetches it) gets the header's address |
| B. A switch per site | Fine-grained | The same proxy ranges in many places; the panel and parked domains need their own switch; easy to forget one site and get mixed logs |
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

**A, A and B.**

### What the admin sets

- A server-wide card "Real client IP" under Settings (admin only), off by default:
  - **Trusted proxies:** a preset "Cloudflare" (the shipped list, shown read-only with its date) and/or own ranges (CIDR or single address, one per line, at most 256).
  - **Header:** `X-Forwarded-For` (default, recursive), `X-Real-IP`, `CF-Connecting-IP`, `True-Client-IP`. Nothing else, so no header name reaches the config as free text.
- The card says plainly: only switch this on if the server is really behind that proxy, and the firewall should then allow ports 80/443 only from the proxy ranges, otherwise visitors can still reach the server directly (they then show their own address, which is correct, but bypass the proxy). Firewall rules stay a separate step (ADR-0033), not part of this ADR.

### Checks (panel and again in the agent)

- Every range is parsed with `net/netip` and rendered from the parsed value.
- Refused: `0.0.0.0/0`, `::/0`, IPv4 prefixes shorter than `/8`, IPv6 shorter than `/16` [A: bounds wide enough for Cloudflare's `/13` and `/29`]; loopback, link-local, unspecified and multicast; any address of the server's own interfaces, private ones included (the agent reads them like `internal/netaddrs`). Private ranges (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`) are allowed, for an own load balancer in a private network.
- A refusal is `422 real_ip_rejected` with the range and the reason.

### nginx rendering

- At the `http` level, only while switched on: one `set_real_ip_from` per range, `real_ip_header <header>;`, and `real_ip_recursive on;` for `X-Forwarded-For`.
- The site access logs (ADR-0053, today nginx's default `combined` format) get a Hostashi `log_format` that is `combined` plus the original peer as an extra field at the end (`$realip_remote_addr`), so an admin can still see which edge a request came through; existing log readers in the panel ignore extra fields [A, checked in the site logs tests].
- PHP gets the real address through the existing `REMOTE_ADDR $remote_addr`; `node`, `app` and `proxy` sites get it in `X-Real-IP` and as the last entry of `X-Forwarded-For`, as today. The panel server keeps sending `X-Real-IP $remote_addr` and clearing `X-Forwarded-For`, so the panel sees the real address without trusting any header itself.
- Snippets (ADR-0029) keep refusing `set_real_ip_from` and `real_ip_header` [V: not on the allowlist today; a test pins it].

### fail2ban

- While switched on, the trusted ranges go into `ignoreip` of the Hostashi jail file, so fail2ban never bans a proxy edge.
- Bans of real visitors still go into nftables but do not stop traffic that comes through the proxy. The panel's own limiter (on the real address) keeps protecting the login. The card says so, and points to the proxy's own tools (for example Cloudflare's WAF rules) for blocking there.

### Storage, API, agent

- Settings keys (no new table, no migration): `realip.enabled`, `realip.cloudflare`, `realip.ranges`, `realip.header`.
- API: `GET` and `PUT /api/v1/settings/real-ip`, action `settings.realip:write`, admin only (ADR-0035), new routes with OpenAPI records (ADR-0058). Every change goes into the audit log with old and new values.
- Agent: `web.Desired` gets a typed `RealIP` struct that `web.apply` checks and renders; the fail2ban jail file is written by the existing fail2ban package with the new `ignoreip` values. No new agent operation, no new `os/exec` exception, `nginx -t` with rollback as today.
- The Cloudflare list lives in Go (`internal/agentapi`, with the date it was fetched). A weekly workflow `Proxy ranges` fetches the three Cloudflare URLs, fails if they disagree, refuses a list that breaks the checks above, and opens or updates a pull request; nothing is merged automatically. Other providers can get presets later the same way.

## Consequences

- Sites, PHP, apps, logs, the audit log and the panel's login limiter see the visitor's address behind Cloudflare or any configured proxy.
- A wrong range lets whoever sends from it choose the client address the server believes. That is the main risk; the checks above, admin-only access and the audit log limit it, and loopback and own addresses can never be trusted.
- fail2ban does not ban proxy edges any more, but also cannot block visitors who come through a proxy. That is a property of the network path, not of this setting.
- Not in v1: PROXY protocol, a per-site opt-out, presets for other providers, automatic firewall rules for the proxy ranges.
- Tests: unit tests for the range checks (too wide, loopback, own address, IPv4-mapped IPv6, duplicates, garbage), template tests with and without the setting and for each header; a container test with real nginx that sends a request from a trusted and from an untrusted address with a forged `X-Forwarded-For` and checks the access log, `REMOTE_ADDR` in PHP and the panel's audit entry.
- Work estimate: 4 small PRs (agent and nginx, fail2ban, settings API, panel card with screenshots first) plus the workflow [E].

## Open questions for Kim

1. Should the setting be server-wide (recommended) or a switch per site?
