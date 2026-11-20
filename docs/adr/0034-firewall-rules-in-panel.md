# ADR-0034 Firewall rules in the panel

**Status:** Proposed. **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0018 (API, scopes), ADR-0021 (fail2ban), ADR-0023 (panel HTTPS), ADR-0033 (hardening baseline, firewall H1), [`03-features.md`](../03-features.md) row "Firewall (nftables/UFW) with defaults" (P0, "SSH lockout protection (rollback timer)").

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Since PR #165 the installer loads one nftables table `inet hostashi` through `hostashi-firewall.service`: inbound drop, open are SSH (every port of `sshd -T` and of open sessions), TCP 80, TCP and UDP 443 and the panel port, plus `--firewall-allow PORT/PROTO` [V: `packaging/install.sh` step `firewall`, `packaging/firewall/`].
- Extra ports today mean re-running the installer with the same flags on every run; there is no way to limit SSH or the panel to known addresses [V].
- Kim decided on 2026-11-20 that firewall rules in the panel come in v1 (ADR-0033, open question 5).
- The agent already starts and reloads units through `systemctl` in the `internal/phpctl` exception, with fixed arguments and unit names that match fixed patterns [V: `internal/phpctl/phpctl.go`].
- `nft -f` applies a file in one transaction: if any line fails, nothing changes [U: nftables semantics; to be checked in the container test].
- The panel knows the client address of each request, from the TCP peer or from nginx on the panel socket only [V: ADR-0023, `clientIP`].

## What the panel manages

1. **Opened ports:** TCP or UDP, one port or a range, open to everyone or only to a list of addresses (IPv4/IPv6, CIDR).
2. **Allowlists for SSH and for the panel port:** when a list is set, only these addresses reach the port; without a list it stays open to everyone, as today.
3. Nothing else in v1: no outbound rules, no rules per site user, no NAT, no rate limits [A: covers the P0 row; outbound per site UID stays a later idea, `04-architecture.md`].

Fixed and never editable: established traffic, loopback, the needed ICMP, DHCP replies, TCP 80 and TCP/UDP 443 (the sites), and fail2ban's own table [A: a panel that can close the web ports breaks every site at once].

## Options for applying the rules

| Option | Pro | Contra |
|--------|-----|--------|
| **A. The agent writes a second file, `hostashi-firewall.service` loads both, the agent reloads the unit through `phpctl`** | No new `os/exec` exception, only one more fixed unit name in an existing one. The same `nft -f` transaction as today. The installer keeps owning the base file | The agent cannot ask `nft -c` before the reload; a rejected file shows only as a failed reload [A: rendered from typed values, so a rejection means a bug] |
| B. The agent talks to nf_tables over netlink (`github.com/google/nftables`) | No external binary at all; the agent can read back the table | New dependency in the root process, much more code, rules as Go structures instead of readable text [U]. Not boring tech |
| C. The agent runs `nft` itself | `nft -c` before applying, readable errors | A new `os/exec` exception for a tool that can change every rule on the host [A] |

## Recommendation

**Option A.**

- **Files.** The installer's `/etc/hostashi/firewall.nft` stays the base and gains one line: `include "/var/lib/hostashi-agent/firewall/panel.nft"`. The agent writes `panel.nft` (root, `0600`), which only adds to the table `inet hostashi`: two sets per allowlist (IPv4 and IPv6) and a chain `panel` that the base input chain jumps to before its own port rules. The installer writes an empty `panel.nft` once and never touches it again, so re-running the installer keeps the panel's rules [A].
- **Rule order.** In `panel`: an allowlist for SSH or the panel drops that port for every address outside the list; then the opened ports accept. After `panel` the base rules run as today. So an allowlist narrows the base rules and an opened port adds to them; nothing in `panel` can close 80 or 443 [A].
- **Typed operation.** `firewall.apply` gets the complete rule list from the panel (desired state in SQLite, as for every other feature) and renders it from a template with checked values only: protocol from a fixed list, ports 1 to 65535 and not 80 or 443, addresses parsed with `net/netip`, at most 50 rules and 256 addresses per list [E]. `firewall.status` returns what is live.
- **Rollback timer.** Every change goes live as *pending*. The agent keeps the previous file and restores it, and reloads, unless the panel confirms within **120 seconds** [E]. The browser confirms by itself with a new request right after the change, so the confirmation proves the admin still reaches the panel through the new rules. A pending change survives an agent restart: the agent rolls back on start if the deadline has passed. One change at a time.
- **Lockout guards before applying.** The panel refuses an allowlist for the panel port or SSH that does not contain the address of the request that sets it, unless the request says so explicitly ("I know, I come in another way") [A]. Loopback is always allowed. The SSH allowlist cannot be confirmed by the browser, so the panel shows a warning to test SSH before the timer runs out [A].
- **API.** `GET /api/v1/firewall`, `PUT /api/v1/firewall` (whole list, returns the pending change with its deadline), `POST /api/v1/firewall/confirm`. New scopes `firewall:read` and the high-risk `firewall:write`. Every change and every rollback goes to the audit log.
- **Installer flags.** `--firewall-allow` stays for the first start and servers without the panel; `--no-firewall` also turns the panel's rules off (the page says so).

## Consequences

- `internal/phpctl` may reload one more fixed unit, `hostashi-firewall.service`; the comment of the exception and its tests name it. No new package with `os/exec`.
- The base file gets the `include` line and the `jump panel`; the firewall container test covers allowlists, opened ports, a rejected change and a rollback, probed from the host as today.
- A panel page "Firewall" with the opened ports and the two allowlists, and the pending state with a countdown.
- Doctor shows the live rules and whether a change is pending.
- `03-features.md`: the firewall row is covered for v1.

## Risks to name openly

- **Lockout through an allowlist.** A wrong list for SSH and the panel locks the admin out until the timer rolls back. If the admin confirms in the browser but SSH is wrong, only the panel is left to fix it; if both are wrong and confirmed, only the provider's console helps [A].
- **Changing addresses.** A home connection with a dynamic IP falls out of the allowlist overnight. The page says so; a VPN or a fixed office address is the better source [A].
- **Same port, two places.** With a provider firewall in front (Kim's setup), a port must be opened in both [A].
- **Not a pentest.** Rules are rendered from typed values and checked in tests; the external review is still open.

## Open questions

1. Rollback after 120 seconds without confirmation? **Decided (Kim, 2026-11-20): yes.**
2. Allowlists only for SSH and the panel port in v1, opened ports with an optional address list? Recommendation: yes.
