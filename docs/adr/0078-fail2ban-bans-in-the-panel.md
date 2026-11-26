# ADR-0078 fail2ban bans in the panel

**Status:** Proposed. **Date drafted:** 2026-11-26. **Decides:** Kim. **Related:** ADR-0021 (fail2ban), ADR-0034 (firewall rules in the panel), ADR-0035 (roles), ADR-0052 (services), ADR-0053 (site logs), ADR-0059 (real IP), ADR-0066 (basic auth and IP rules).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

fail2ban runs on every server with the jails `sshd`, `hostashi-panel`, `hostashi-site-auth` and `recidive` (ADR-0021) [V: `packaging/fail2ban/hostashi.conf`]. The panel shows only whether the service runs (Services page, doctor check `service.fail2ban` over its socket) [V: `internal/agent/doctor.go`]. Everything else needs SSH and `fail2ban-client`.

Two cases from practice:

- **A customer is locked out.** Ten wrong passwords on a password-protected site (for example a staging copy, ADR-0070) ban the visitor for one hour on ports 80 and 443; three such bans in a day ban them for a week on all ports through `recidive` [V: jail settings]. Today only the admin's shell can lift that.
- **A jail that silently counts nothing.** On web3 the jail `hostashi-site-auth` counted zero failures for a day because nginx's workers kept writing to rotated logs (fixed in PR 595) [V: web3, 2026-11-26]. "0 failed" next to a running jail was the only sign, and nobody saw it without SSH.

Constraints:

- Security before features: the panel runs without root; the agent runs typed operations only. No `os/exec` exception exists for fail2ban today; the never-ban list of trusted proxies is written as a file and fail2ban is reloaded through systemd [V: `internal/f2bproxies`, `internal/agent/agent.go`]. A new `os/exec` exception needs an ADR (project rule 1).
- fail2ban's own socket speaks Python pickle [U]; reimplementing that protocol in Go is not boring tech.
- fail2ban keeps its bans in a SQLite file, `/var/lib/fail2ban/fail2ban.sqlite3`, table `bans` [U: default `dbfile`]. It is an internal format of fail2ban.

## Options

**Part 1: what the panel shows and does**

| Option | Pro | Contra |
|---|---|---|
| A. Status only (jails, counters, banned addresses) | Read-only, no risk | The lock-out case still needs SSH |
| **B. Status plus unban of one address** | Covers both cases; unban only lifts a block, it never blocks anyone | One write operation, which needs audit and a role check |
| C. B plus editing jail settings (maxretry, bantime, own jails) and manual bans | Full control | A settings UI for fail2ban to maintain; wrong values can lock out the admin or make the jails useless; manual bans overlap with the firewall rules of ADR-0034 |

**Part 2: how the agent talks to fail2ban**

| Option | Pro | Contra |
|---|---|---|
| **A. `fail2ban-client` with fixed arguments in a new package `internal/f2bctl`** | The documented interface of fail2ban [U]; works the same on Debian 13 and Ubuntu 26.04; small | New `os/exec` exception; parses text output, so a test pins the format |
| B. Read the SQLite file, unban through the client | No exec for reading | Two ways to one daemon; the file is an internal format and lags behind the live state [A]; unban still needs exec |
| C. Speak the socket protocol (pickle) | No exec | Unpickling data in Go, own protocol code; not boring |

## Recommendation

**1B + 2A.**

**What the panel shows (Firewall page, new card "Blocked addresses").** For each Hostashi jail: whether it runs, currently failed, total failed, currently banned, total banned, and the banned addresses with the time of the ban and when it ends. Shown to admins only, like the rest of the Firewall page (`firewall:read`). A short line explains each jail in plain words ("SSH logins", "Panel logins", "Password-protected sites", "Repeat offenders").

**What the panel does.** "Unblock" next to each address, for admins (`firewall:write`), with a confirmation that names the address. It lifts the ban in that jail; an address banned by `recidive` and by a site jail is unblocked in both with one action, because otherwise the button seems to do nothing [A]. Every unblock goes into the audit log with jail and address. There is no manual ban and no settings editor; thresholds stay in the installer's file, and an own `jail.d` file stays the way to change them (ADR-0021).

**Health.** A doctor warning when one of the four jails is not running, linking to the Firewall page. A jail that runs and counts zero is normal on a quiet server, so it does not warn [A].

**Agent.** Two typed operations:

- `fail2ban.status` (read): returns the four jails. It runs `fail2ban-client status <jail>` and `fail2ban-client get <jail> banip --with-time` for each jail name from a fixed list in the code; never a jail name from the request.
- `fail2ban.unban` (write): request `{jail, address}`. The jail must be one of the four; the address is parsed with `netip.ParseAddr` and passed in canonical form, so no other string reaches the command line. It runs `fail2ban-client set <jail> unbanip <address>`.

`internal/f2bctl` is the only package allowed to start `/usr/bin/fail2ban-client`, by absolute path, with these three argument shapes, a timeout and a size limit on the output. It is added to the `no-exec` exceptions in `.golangci.yml` with a comment pointing here. No shell, no other sub-command.

**API and CLI.** `GET /api/v1/firewall/bans` and `DELETE /api/v1/firewall/bans/{jail}/{address}`, described in OpenAPI (ADR-0058), and `hostashi firewall bans` / `hostashi firewall unban <jail> <address>`.

## Consequences

- One new `os/exec` exception (`internal/f2bctl`), limited to fixed sub-commands of `fail2ban-client`. No new table or migration.
- The output of `fail2ban-client` becomes an interface Hostashi depends on. Unit tests pin it with captured output of fail2ban 1.1 (Debian 13: 1.1.0-8 [V: package]); the installer test checks status and unban against the real daemon, extending the ban check of PR 595.
- Addresses are personal data. They are shown only to admins, are not stored by Hostashi, and appear in the audit log only for an unblock [A: same as the firewall rules today].
- An admin can unblock an attacker by mistake; the ban comes back after the next failures. That is the same risk as `fail2ban-client` on the shell.
- Behind Cloudflare a ban on the server does not stop the visitor, because the traffic comes from Cloudflare's addresses (ADR-0059). The card says so when a site trusts the Cloudflare preset [A].
- Work: agent package and operations, API, CLI, UI card, doctor check, wiki page "Firewall", tests [E: 4 to 5 small PRs].

## Open questions for Kim

1. Is the Firewall page the right place, or should the card live under Health? Recommendation: Firewall, because Health only shows states (rule from PR 492) and this card has an action.
2. Unblock in all jails at once (recommended) or per jail?
