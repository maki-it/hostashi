# ADR-0042 Panel on port 443 under its own host name

**Status:** Proposed. **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0021 (fail2ban), ADR-0023 (panel HTTPS, port 8443; this ADR adds an option and does not change its default), ADR-0034 (firewall), ADR-0018 (optional IP allowlist for the panel).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants to run the panel directly under a host name like `https://cp.maki-it.de/`, without a port, as with CloudPanel today [V: Kim, thread, 2026-11-20].
- Today nginx serves the panel on its own port, 8443 by default, and the installer refuses `--panel-port 80` and `443` because the sites use them [V: `packaging/install.sh`, `internal/agent/agent.go`, ADR-0023].
- The panel port has its own default server that refuses the TLS handshake for unknown names (`ssl_reject_handshake on`), and the sites' port 443 has one too [V: `internal/web/nginx.go`].
- The host firewall restricts the panel ports to the panel allowlist (`panel_ports`, `panel_allow4/6`) [V: `packaging/firewall/hostashi.nft`, `internal/firewall/firewall.go`]. A packet filter cannot see the host name in TLS, so on a shared port 443 it cannot tell panel from site traffic [U].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Installer option `--panel-port 443`; 8443 stays the default** | What Kim wants; existing installs unchanged | Two code paths for the panel listener; the IP allowlist moves into nginx for 443 |
| B. 443 as the new default | One path | Every install shares the port with the sites, and the allowlist is always in nginx [A] |
| C. Keep 8443 only; document a reverse proxy in front | No code | Not what Kim wants; extra moving part |

## Recommendation

**Option A.** Decided in the thread (Kim, 2026-11-20): installer option, 8443 stays the default, allowlist in nginx on 443.

- **Installer.** `--panel-port 443` is accepted (80 stays refused). Example: `install.sh --panel-domain cp.maki-it.de --panel-port 443`.
- **`--panel-domain` replaces `--public-url`.** Today the installer takes a URL and adds the port from `--panel-port` itself, so the two options repeat each other and can disagree [V: `packaging/install.sh`]. The panel needs the exact origin for passkeys [V: `internal/serve/serve.go`]; it is derived as `https://<domain>` on 443 and `https://<domain>:<port>` otherwise. There is no release yet, so `--public-url` is removed without an alias [A: Kim to confirm].
- **nginx.** With 443, the panel gets no listener of its own: its server block listens on the sites' TLS addresses with `server_name` = panel host name, and the panel's own "reject unknown names" server is not rendered (the sites' default server does that job). HTTP/3, HSTS and TLS settings stay the same; `Alt-Svc` names port 443. Port 80 redirects the panel name to `https://<name>/`.
- **Reserved name.** The panel host name cannot be a domain, alias or parked domain of a site; the API answers `409 domain_reserved`. The installer and doctor check that no existing site already uses it.
- **IP allowlist.** On 443, `panel_ports` in the firewall does not contain 443. The allowlist is enforced by nginx in the panel's server block (`allow` per address, then `deny all`), generated from the same setting. On 8443 nothing changes: the firewall does it.
- **fail2ban.** The `hostashi-panel` jail keeps watching panel logins. On 443, a ban also blocks that address from the sites, because the ban works on the port. That is accepted: the address was guessing panel passwords [A].
- **Changing later.** `hostashi panel port <port>` switches between 8443 and 443 on an installed server (nginx, firewall, public URL) [A: small command, same checks as the installer].

## Consequences

- `internal/web` renders the panel either on its own listeners or inside the 443 set; tests for both.
- `internal/firewall` leaves 443 out of `panel_ports`; tests.
- Docs: install guide with the cp.maki-it.de example and the DNS record to set.

## Risks to name openly

- **Allowlist in nginx instead of the packet filter.** Clients outside the allowlist reach nginx's TLS handshake on 443 (as they do for every site) before they get `403`. That is weaker than a dropped packet on 8443 [A].
- **Shared fail2ban bans** block sites for a banned address (see above).

## Open questions

None; decided by Kim in the thread on 2026-11-20. Status stays Proposed until Kim accepts the text.
