# ADR-0021 Brute-force and intrusion protection

**Status:** Accepted (2026-11-18, by Kim). **Date drafted:** 2026-11-18. **Decides:** Kim. **Related:** ADR-0003 (stack), ADR-0004 (web/agent privilege split), ADR-0005 and ADR-0020 (nginx), ADR-0009 (target platforms), [`05-security.md`](../05-security.md) sections 5.2 to 5.4, [`03-features.md`](../03-features.md) (rows "Fail2ban-/CrowdSec-Integration" and "Firewall").

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Internet scanners and bots that brute-force logins are the first attacker group in the threat model [V: `05-security.md` 5.2].
- The panel already throttles logins in process: 10 failures per IP and 20 per user within 15 minutes, 30 unknown API tokens per IP within 15 minutes, answered with HTTP 429 [V: `internal/auth/service.go`, `internal/auth/limiter.go`]. The state is in memory and resets on restart [V: `limiter.go`].
- The panel takes the client IP from the TCP peer and ignores forwarded headers [V: `internal/serve/auth_api.go`, `clientIP`].
- Failed logins are written to the audit log in SQLite as `login.failed` with the IP [V: `internal/auth/service.go`]. They are **not** written to the process log or the journal. The process log is JSON on stderr, and it only reports internal errors for auth [V: `cmd/hostashi/main.go`, `internal/serve/auth_api.go`].
- nginx writes one global `access_log /var/log/nginx/access.log` for all sites, there are no per-site logs yet [V: `internal/agent/agent.go`, `internal/web/nginx.go`].
- Hostashi does not manage a firewall yet. nftables is planned and owned by the agent's reconciler [V: `04-architecture.md`, `03-features.md`].
- The in-process limiter only protects the panel. It does not cover SSH, the sites (for example `wp-login.php`, `xmlrpc.php`) or scanners that hit many sites, and it cannot drop traffic before it reaches a process [A].
- `03-features.md` lists "Fail2ban-/CrowdSec-Integration" as P1 [V].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. fail2ban from the distribution, on by default** | Distribution package on Debian 13 and Ubuntu 26.04 [U], no external service, no account, widely known and documented, nftables ban actions included [U]. Fits boring tech | Only local knowledge: an IP is banned after it has already tried here. Python daemon with regex filters, which costs some CPU on busy logs [U]. Filters are text, and badly written ones can be tricked by log injection [U] |
| B. CrowdSec instead of fail2ban | Community block list, so known bad IPs are blocked before they try. Parsers and scenarios for many services [U] | Sends signals (attacking IPs, scenario names) to the CrowdSec central API by default, which is a data flow to an external company [U]. Two parts (engine and bouncer), more moving pieces, an account for the console. The distribution packages lag behind upstream, the upstream repository is a third-party apt source [U] |
| C. Only the panel's own rate limit | Nothing to install | SSH and sites stay unprotected. Every request still reaches the process |
| D. fail2ban now, CrowdSec as a later opt-in | A safe local default, and the community list for those who want it and accept the data flow | Two integrations to maintain later [A] |

## Recommendation

**Option D, with option A as the v1 scope.** fail2ban from the distribution is installed and on by default in v1. CrowdSec is not part of v1. It can come later as an opt-in that replaces fail2ban on that server, not as a second engine next to it [A: two engines banning the same IPs make the state hard to explain].

Defaults for v1:

- **Jails on by default:** `sshd` and `hostashi-panel` (failed panel logins, failed second factor, unknown API tokens).
- **Jail `recidive`** on by default, for IPs that are banned again and again [U: shipped with fail2ban].
- **Per-site jails** (for example WordPress login and `xmlrpc.php`) are available but off by default, because false positives hit real visitors of a customer site [A]. They need per-site nginx logs (see consequences).
- **Backend:** the systemd journal for `sshd` and the panel. Debian 12 and later do not install rsyslog by default, so `/var/log/auth.log` may not exist [U].
- **Ban action:** nftables, in fail2ban's own table [U].
- **Never banned:** loopback (`127.0.0.0/8`, `::1`) and the panel's admin IP allowlist from ADR-0018 and `05-security.md` 5.3, so an admin cannot lock themselves out [A].
- **Ban times:** 1 hour for a first ban, longer through `recidive` [E].

The panel's own rate limit stays. It answers inside the request and works without fail2ban, fail2ban adds the network layer and the services outside the panel [A].

## Consequences

- **Structured log line for failed logins.** The panel writes one JSON line per failed login, failed second factor, failed passkey login and unknown API token to the journal (stderr of `hostashi.service`), with a fixed event name and the IP as its own field, for example `{"msg":"auth.failed","kind":"password","ip":"203.0.113.7"}`. The username is not logged in this line, only in the audit log [A: keeps personal data out of the journal]. The filter matches the fixed event name and the `ip` field only, never free-text fields, so a crafted username cannot inject an IP [A]. Tests cover the line format, because the fail2ban filter depends on it.
- **Per-site nginx logs.** The reconciler writes one access log and one error log per site, `/var/log/nginx/sites/<site>/access.log` and `error.log` [V: decided by Kim]. The directory is owned by root and not writable by the site user, otherwise a compromised site could write fake lines and get any IP banned, including the admin's [A]. Logrotate covers the new paths. This also serves the site log view in the panel, which is needed anyway [A]. CrowdSec would read the same files.
- **Firewall reconciler and fail2ban share nftables.** When Hostashi manages nftables, its reconciler must only replace its own table and never run `flush ruleset`, or it removes fail2ban's bans [U: nftables semantics]. Test this once the firewall reconciler exists.
- **Typed agent operations, no general exec.** fail2ban runs as its own root daemon from the distribution and runs its ban commands itself [U]. Hostashi does not pass commands to it. The agent only gets typed operations: write the Hostashi jail and filter files from fixed templates with validated values (jail names from a fixed list, numbers in ranges, IPs and CIDRs parsed), list bans, and unban one validated IP. Talking to `fail2ban-client` needs `os/exec` and is a second exception next to `internal/sysuser`, with fixed arguments and no shell [A]. This is a named conflict with "no exec", accepted by Kim on 2026-11-18. The exception lives in one package (for example `internal/fail2ban`), and depguard keeps `os/exec` out of every other package [A: package name open]. Alternative: speak fail2ban's socket protocol directly, which avoids exec but relies on an internal, Python-pickle-based protocol [U], so not recommended.
- **Panel UI and API.** List banned IPs, unban, edit the never-ban list, switch per-site jails. Unbanning and the never-ban list are high-risk actions with their own scope (ADR-0018).
- **Installer.** Installs `fail2ban` from the distribution archive, writes the Hostashi jail files under `/etc/fail2ban/jail.d/` and filters under `/etc/fail2ban/filter.d/`, and checks the setup with `fail2ban-client -t` [U: test option]. CI runs this on Debian 13 and Ubuntu 26.04 in the systemd container [A].
- **Panel behind a proxy.** If the panel ever runs behind nginx (option in `05-security.md` 5.3), the TCP peer is `127.0.0.1` for every client. Then both the in-process limiter and fail2ban see the wrong IP. That setup needs a trusted-proxy setting first [V: `clientIP` uses the TCP peer].
- `03-features.md`: "Fail2ban-/CrowdSec-Integration" is split into "fail2ban" (P0) and "CrowdSec opt-in" (P2).

## Risks to name openly

- **Self-lockout.** A wrong never-ban list or a shared office IP can lock the admin out of SSH and the panel. Mitigation: never-ban list, loopback always excluded, ban times that expire, and the Hetzner console as a fallback [A].
- **Banning by shared IPs.** Carrier-grade NAT and company networks put many users behind one IP, so a ban can hit innocent visitors. That is the main reason why per-site jails are off by default [A].
- **Log injection.** fail2ban filters are regexes over text. Fields that attackers control (user agent, URL, username) must not be able to look like an IP in the matched position [U]. Our own filters match fixed JSON fields, the distribution's `sshd` filter is maintained upstream.
- **CrowdSec data flow.** If CrowdSec comes later, attacker IPs and scenario names go to an external service by default. That needs a short note in the privacy documentation and an explicit opt-in [U].
- **Not a pentest.** This ADR describes a layer against automated guessing. It does not replace the external review planned in `05-security.md`.

## Open questions

Decided (Kim, 2026-11-18): fail2ban is on by default with the `sshd`, `hostashi-panel` and `recidive` jails, the installer does not ask.

Decided (Kim, 2026-11-18): per-site nginx logs live under `/var/log/nginx/sites/<site>/`, owned by root and not writable by the site user.

Decided (Kim, 2026-11-18): the agent talks to fail2ban through `fail2ban-client`, as a second `os/exec` exception next to `internal/sysuser`, with fixed arguments and no shell. The socket protocol is not used. Kim also confirmed fail2ban over an own ban engine in Hostashi, because that engine would parse attacker-controlled logs as root and rebuild fail2ban.

No open questions remain.
