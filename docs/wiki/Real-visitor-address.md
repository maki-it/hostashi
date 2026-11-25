# Real visitor address

Behind a CDN such as Cloudflare or behind a load balancer, every request seems to come from the proxy. The site's logs, PHP's `REMOTE_ADDR`, the `X-Real-IP` of apps and proxied sites, and the maintenance allowlist then all see the proxy's address instead of the visitor's.

With the setting on, nginx takes the visitor's address from a header, but only for connections that come from the proxies you trust. A request from anywhere else keeps its own address, whatever headers it sends. The setting is per site and off by default.

**For:** everyone who can change the site; own proxy addresses only admins.

## Switch it on

1. Open the site and go to the **Domains & HTTPS** tab.
2. Under **Real visitor address**, tick **This site is behind a proxy or CDN**.
3. Choose the proxies to trust:
   - **Cloudflare** trusts Cloudflare's published edge ranges. The card shows how many there are and from which day the list is.
   - **Own proxies** are addresses or ranges of your own load balancer or another CDN, one per line, at most 256.
4. Keep the header at **X-Forwarded-For** unless your proxy sends the address somewhere else.
5. Click **Save**. nginx uses the setting right away.

Only switch this on if the site is really behind that proxy. Visitors who reach the server directly, past the proxy, still show their own address. If the site must only be reached through the proxy, let ports 80 and 443 through only from the proxy, in your own firewall or the server's.

## Headers

| Header | When |
|--------|------|
| `X-Forwarded-For` (recommended) | Cloudflare and nearly all other proxies and load balancers. nginx takes the rightmost address that is not a trusted proxy, so addresses a visitor writes into the header themselves do not count. |
| `X-Real-IP` | Proxies that send one address there, for example some nginx setups. |
| `CF-Connecting-IP` | Cloudflare's own header. |
| `True-Client-IP` | Akamai and Cloudflare Enterprise. |

With a chain, for example your own load balancer behind Cloudflare, every hop must be trusted: tick Cloudflare and add the load balancer under **Own proxies**.

## Own proxy addresses

Only admins add own proxy addresses, because they also go into fail2ban's never-ban list for the whole server (see below). Other users see the addresses read-only and can remove them.

The panel refuses ranges that must never be trusted:

- wider than `/8` (IPv4) or `/16` (IPv6),
- loopback, link-local, multicast and unspecified addresses,
- any address of this server itself, private ones included. Every site user can connect to those with any header, so trusting one would let a site user choose the address another site sees.

Private ranges such as `10.0.0.0/8` or `fd00::/8` are fine for a load balancer in a private network.

## The access log

A site with the setting on writes the proxy's own address at the end of each access log line, as `proxy=…`, so you can still see which edge a request came through. The [site logs](Site-logs.md) view shows the lines as before.

## fail2ban

fail2ban never bans a trusted proxy, because a ban of one Cloudflare edge would block every visitor who comes through it. This also means that fail2ban's bans do not stop visitors who come through the proxy: the ban hits the visitor's address, but the connection comes from the proxy. The panel's own login limiter still works on the real address. Block unwanted visitors in the proxy itself, for example with Cloudflare's WAF rules.

## The panel's own address

If the panel's host name is behind a proxy too, admins set the same on the [**System**](System-settings.md) page under **Real visitor address of the panel**. The panel's audit log, its login limiter and the panel allowlist then see the visitor's address.

## How the Cloudflare list stays current

The list ships with Hostashi; servers never download it. Once a week a workflow compares it with Cloudflare's published lists and opens a pull request when they change. A maintainer reviews it, and the next release brings the new list.

## API

- `GET` and `PUT /api/v1/sites/{name}/real-ip` with a body like `{"cloudflare": true, "ranges": ["10.0.0.2"], "header": "X-Forwarded-For"}`; `{}` switches the setting off. Tokens need `sites:read` or `sites:write`.
- `GET` and `PUT /api/v1/settings/panel-real-ip` for the panel, admins in the browser only.

A refused range answers `422 real_ip_rejected` with the reason. Every change goes into the [audit log](Audit-log.md).
