# ADR-0038 Web database tool with short-lived access

**Status:** Accepted (Kim, 2026-11-20); Addendum 1 Proposed. **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0023 (panel HTTPS through nginx), ADR-0024 (MariaDB per site, no stored passwords), ADR-0025 (database import and export), ADR-0033 (hardening baseline), ADR-0034 (firewall), ADR-0035 (roles), ADR-0037 (UI redesign).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants to browse and edit database contents (for example single rows) from the panel, in v1 [V: Kim, thread, 2026-11-20].
- `03-features.md` lists "Web DB tool with SSO (Adminer or phpMyAdmin)" as P1 with the rule "only short-lived tokens, never master credentials in the browser" [V]. CloudPanel ships phpMyAdmin [V: `01-cloudpanel-research.md`].
- Hostashi never stores database passwords; a new password is shown once (ADR-0024) [V].
- The agent talks to MariaDB as root over the Unix socket with the Go driver, without a process [V: `internal/agent`, memory of PR 70].
- The panel is served by nginx on port 8443 and proxies to `127.0.0.1:8080` (ADR-0023) [V]. Its API refuses unsafe requests whose `Sec-Fetch-Site` is not `same-origin`, or whose `Origin` is another host; session cookies are `HttpOnly`, `Secure`, `SameSite=Strict` [V: `internal/serve/auth_api.go`, `secure`, `sameOrigin`].
- Adminer: latest upstream release 6.3.0 (6.3.1 since 2026-11-20, which Hostashi ships [V: adminer.org, 2026-11-21]), offered as a single PHP file, also as a MySQL-only English build (`adminer-6.3.0-mysql-en.php`) [V: adminer.org, 2026-11-20]. Debian 13 packages 5.2.1, Ubuntu 26.04 packages 5.4.1 [V: sources.debian.org, packages.ubuntu.com, 2026-11-20]. Licensed Apache-2.0 or GPL-2.0 [U: adminer.org].
- phpMyAdmin is a much larger PHP application with many files and its own configuration storage [U].

## Options

### Tool

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Adminer, upstream MySQL-only single file, pinned by SHA-256 in each Hostashi release** | One file; only the MySQL driver, so no SQLite or file-based drivers to abuse; current version; easy to wrap for a ticket login [A] | Hostashi has to ship updates itself; a security fix needs a Hostashi release [A] |
| B. Adminer from the distribution package | Security updates through unattended-upgrades (ADR-0033) | Older versions (5.2.1 on Debian 13); all drivers included; the package installs its own web server snippets that we would have to disable [U] |
| C. phpMyAdmin | Known to many users; CloudPanel uses it | Much larger attack surface; needs its own storage and configuration [U] |
| D. Our own table editor in the panel | No PHP, fits the panel's design | Months of work to do safely (types, large tables, editing, SQL); out of scope for v1 [E] |

### Where it runs

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Own origin: nginx on port 8444 (same certificate as the panel), own PHP-FPM pool under its own system user** | A bug in Adminer (for example XSS) cannot read the panel's pages or call its API, because a different port is a different origin and the API refuses cross-origin writes [V: `sameOrigin`] | One more port in the firewall (same allowlist as the panel) |
| B. Under the panel origin, `/adminer/` | No extra port | An XSS in Adminer runs with the panel's origin and can drive the panel API with the admin's session [A] |

## Recommendation

**Tool A, place A.**

- **Install.** The installer puts the pinned file under `/usr/share/hostashi/adminer/` (root, read-only) with a small wrapper `index.php` written by Hostashi (AGPL) that extends Adminer through its plugin interface: login only by ticket, the server fixed to the local socket, only the one database of the ticket shown, no permanent login. Turning it off is a setting (`--no-db-tool`, and a switch in the panel).
- **Process.** Its own PHP-FPM pool and unit under the system user `hostashi-adminer`: no home, no shell, `open_basedir` limited to its own directory and its private `/tmp`, the same `disable_functions` as sites (ADR-0033), the unit sandbox of a site pool, at most 4 workers [E]. It reaches MariaDB only through the socket.
- **nginx.** A server on port 8444 for the panel host name, same certificate and TLS settings as 8443, same IP allowlist (ADR-0018/0034), strict headers (`frame-ancestors 'none'`, `Referrer-Policy: no-referrer`), fail2ban watches it like the panel (ADR-0021). (Implementation: the tool has no login form, only 128-bit one-time tickets, so there are no failed logins for fail2ban to count; no jail is added.) The firewall opens 8444 only while the tool is on.
- **Access flow.**
  1. In the panel, the user clicks "Open in Adminer" on a database. The panel checks the role (ADR-0035: admin and manager for all sites, user only for granted sites; new action `databases:open`, read-only variant for `user` if Kim wants).
  2. The agent creates a temporary MariaDB user `hostashi_tool_<random>` with a random password, granted only on that database (`ALL` without `GRANT OPTION`, or `SELECT` for read-only), `MAX_USER_CONNECTIONS 2`, from `localhost` only. No new `os/exec`: plain SQL like the existing database code. (Implementation: the prefix is `hostashi_tool_` instead of the drafted `hst_t_`, because `hostashi` is already reserved for site users, so no migrated account can collide.)
  3. The agent writes a one-time ticket (database, user, password, expiry) into `/run/hostashi-adminer/tickets/<id>` (`0600`, owner `hostashi-adminer`) and returns only the ticket id.
  4. The browser opens `https://<host>:8444/?ticket=<id>` in a new tab. The wrapper reads and deletes the ticket, keeps the credentials in its own PHP session, and logs in. The password never reaches the browser.
  5. After 30 minutes (or 2 hours, chosen in the dialog) the agent drops the user and kills its connections; the ticket expires after 60 seconds if unused. At start the agent drops every leftover `hostashi_tool_` user.
- **Audit.** Every opening is logged with user, site, database, mode and duration.
- **UI.** A table icon in every database row (ADR-0037), the dialog from the mockup.

## Consequences

- One more PHP pool, unit, nginx server and firewall port; doctor checks them and that no `hostashi_tool_` user outlives its expiry.
- Hostashi releases carry the Adminer file and its hash; Renovate or a release checklist watches new Adminer releases.
- Adminer's license (Apache-2.0 or GPL-2.0) is compatible with shipping it next to AGPL code; REUSE needs an entry for the file [U: no legal review].

## Risks to name openly

- **More attack surface.** Adminer is a PHP application that runs SQL from the browser. A bug in it can read or change the one database of the ticket. The separate origin, the pool sandbox and the per-database user limit what it can reach, but do not remove the risk [A].
- **Full SQL.** With write access the user can run any SQL on that database, including `DROP TABLE`. That is the point of the tool; backups are the safety net.
- **Update lag.** A security fix in Adminer reaches servers only with a Hostashi release (option A). If that is too slow, option B is the fallback.
- **Not a pentest.**

## Open questions

1. ~~Adminer as a single pinned file, on its own port 8444, with a temporary user per opening?~~ **Decided (Kim, 2026-11-20): yes.**
2. ~~May the role `user` open it with write access, or only read-only?~~ **Decided (Kim, 2026-11-20): write access, on granted sites.**

## Addendum 1: extend and end sessions

Asked for by Kim (2026-11-21) together with the panel's look in Adminer: a session that is about to end can be extended from the panel, and any open session can be ended at once.

- **Panel.** The database page shows a card "Adminer sessions" while a session on that database runs: who opened it, when, how long it still runs, with two actions, "Extend by 30 minutes" and "End now". Whoever may open the database (`databases:open`) may see, extend and end its sessions, also those another account opened. Both actions are audited (`database.tool.extend`, `database.tool.end`) [V: `internal/hosting/dbtoolsessions.go`].
- **Limits.** One extension adds 30 minutes; a session never ends more than 2 hours ahead, however often it is extended [V: `agentapi.DBToolExtendStep`, `agentapi.DBToolMaxRemaining`].
- **Who opened it.** The table `db_tool_sessions` (migration 0042) records the account and the times. Whether a session still runs comes from the agent (local) and `db_remote_tools` (external servers); rows of sessions that ended otherwise drop out when the panel lists them [V].
- **Agent.** Three operations, `db.tool.extend`, `db.tool.end` and `db.tool.sessions`, plain SQL and file writes, no new `os/exec` [V: `internal/agent/dbtoolsession.go`]. Extending moves the timer that drops the user. Ending first marks the session ended for the tool, then drops the user and kills its connections. On an external server the panel drops the user, as at the regular end.
- **The open tab.** The agent writes each session's end to `/run/hostashi-adminer/sessions/<user>` (`root:hostashi-adminer`, `0640`, directory `0750`), `0` once it was ended. Hostashi's `index.php` reads it on every request, so an extended session keeps working and an ended one is refused at the next click. The tool can only read the file [V: `packaging/adminer/index.php`].
- **In Adminer.** The top bar has a "+30 min" link next to the remaining time. It opens the database in the panel, where the session is extended; the tool's pool gets no way to talk to the agent.
