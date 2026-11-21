# ADR-0053 Site logs in the panel

**Status:** Accepted (2026-11-21, by Kim). **Date drafted:** 2026-11-21. **Decides:** Kim. **Related:** ADR-0021 (brute-force protection, per-site nginx logs), ADR-0018 (API), ADR-0033 (hardening), ADR-0035 (users and roles), [03-features](../03-features.md) section 3.0 (scope of release 1.0).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- "View/tail logs (access, error, PHP, app)" is a P0 row of the feature matrix and part of release 1.0 [V: `docs/03-features.md`].
- ADR-0021 decided one access log and one error log per site under `/var/log/nginx/sites/<site>/`, owned by root and not writable by the site user, so a site cannot write fake lines that get an address banned [V: ADR-0021]. **It is not built yet**: nginx still writes one `error.log` and one `access.log` for all sites [V: `internal/agent/agent.go`, `internal/web/nginx.go`].
- PHP-FPM runs one master per site (`hostashi-php-<version>@<site>`). Its pool sets `error_log = syslog` with the ident `hostashi-php-<site>` and `catch_workers_output = yes`, so PHP errors of a site end up in the journal [V: `internal/php/php.go`]. The unit has `ProtectSystem=strict`, so it cannot write below `/var/log` today [V: `packaging/systemd/hostashi-php@.service`].
- The agent offers only typed operations; a response frame is at most 64 KiB [V: `internal/agentrpc/protocol.go`]. The web process runs without root and cannot read `/var/log/nginx` [V: ADR-0001, ADR-0021].
- Reading the journal from Go needs either cgo (`sd-journal`), a new `os/exec` exception for `journalctl`, or a reader of the journal file format written by hand [U]. None of these is boring.
- Access logs hold visitor IP addresses and URLs, which are personal data under the GDPR; they should not be kept longer than needed [U: no legal check].
- Node.js and Python sites ("app" logs) are not built yet; they come with their own ADRs and plug into the same view.

## Options

How the PHP log is stored:

| Option | Pro | Contra |
|--------|-----|--------|
| **A. The PHP-FPM master writes a file, `/var/log/hostashi/php/<site>/error.log`** | Same reading path as nginx, rotation by logrotate, no new `os/exec`, the site user cannot write the file directly | The unit needs `LogsDirectory=`; the PHP errors leave the journal |
| B. Keep the journal, read it with `journalctl` | No change to PHP-FPM | New `os/exec` exception with an ADR of its own, output parsing, the journal mixes sites by ident only |
| C. Keep the journal, read the journal files in Go | No exec | Hand-written parser of a binary format, high risk for little gain |

How the panel follows a log live:

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Polling with a cursor: the UI asks every 2 s for lines after its cursor** | Fits the request/response RPC and the 64 KiB frame, no open connection per viewer in the agent, works behind every proxy | Up to 2 s delay, one small request per viewer every 2 s |
| B. A stream from agent to panel to browser (Server-Sent Events) | Lines appear at once | The RPC has no streams; a long-lived agent connection per viewer; more code in the root process |

## Recommendation

**Option A for both.**

### Files

| Log | Path | Written by |
|-----|------|-----------|
| access | `/var/log/nginx/sites/<site>/access.log` | nginx master (root), format `combined` |
| error | `/var/log/nginx/sites/<site>/error.log` | nginx master (root), level `warn` |
| php | `/var/log/hostashi/php/<site>/error.log` | PHP-FPM master (root) through `LogsDirectory=hostashi/php/%i` |

- Directories are `root:root 0750`. The agent creates the nginx directory before `web.apply` and removes it when the site is deleted; systemd creates the PHP one. The site user can neither write nor read them directly; the panel is the way to read them.
- The server-wide `error.log` and `access.log` stay for requests that match no site.
- The PHP pool gets `error_log = /var/log/hostashi/php/<site>/error.log` instead of syslog. A site's own `.user.ini` may still send PHP errors to a file in its home; then the panel shows only what reaches the master [A].
- logrotate (installed by the installer if missing) rotates all three daily, compresses with `delaycompress`, and tells nginx (`USR1`) and the PHP-FPM masters (`systemctl kill -s USR1`) to reopen their files. fail2ban and a later CrowdSec read the same nginx files (ADR-0021).

### Retention, set in the panel

- Kim wants to set the retention in the panel, because it must match what the data processing agreement (AVV) and the privacy policy promise; Kim's say 7 days (Kim, 2026-11-21) [V].
- One server-wide value in days, **default 7, range 1 to 365**. It lives in the existing `settings` table (no migration) and is changed by admins only (`settings:write`, browser session, like the other panel settings).
- The panel sends the number to a typed agent operation `logs.retention.set {days}`. The agent checks the range and renders `/etc/logrotate.d/hostashi-sites` from a fixed template (`rotate <days>`, `maxage <days>`); nothing else comes from the panel. On start the panel sends the stored value again, so a fresh install or a restored database ends up with the same file.
- Lowering the value takes effect at the next logrotate run (daily); logrotate then deletes the files older than the new value. The settings page says so.
- The value covers the three site logs and the server-wide nginx logs of Hostashi. The journal (panel, agent, fail2ban) keeps the distribution's settings; the settings page says that too, so nobody reads 7 days as "every log on this server".
- One value per site is not in v1: the processing agreement is one per server operator [A].

### Agent operation `site.logs.read`

- Parameters: site name (checked like every site name), log kind from the closed set `access`, `error`, `php`, and either `tail` (last N lines, N at most 1,000) or a cursor from an earlier answer.
- The agent builds the path itself from the kind and the name; no path comes from the panel. It opens the file through `os.OpenRoot` on the log directory, refuses symlinks and anything but a regular file owned by root.
- The answer holds at most 48 KiB of lines, each cut at 8 KiB, invalid UTF-8 and control characters except tab replaced, plus a cursor (inode and offset). When the inode changed (rotation) or the file got shorter, the next read starts at the beginning of the new file and says so (`rotated: true`).
- A missing file is an empty answer, not an error: a new site has no log yet.

### Panel and API

- `GET /api/v1/sites/{name}/logs/{kind}?lines=200` and `?cursor=…`. New action `sites.logs:read`, rule `ownSite("logs:read")`: everyone with a grant for the site, as for the file manager, so a user sees the logs of their own sites only. Tokens limited to sites may read their sites' logs. New token scope `logs:read`.
- The site page gets a tab "Logs": kind switch, a "Follow" toggle (polls every 2 s while the tab is visible), pause, a text filter in the browser, and a download of what is loaded. Lines are shown as text, never as HTML.
- Only the current file is shown in v1. Older rotated files stay on disk for fail2ban and the admin's shell.

## Consequences

- No new `os/exec` exception and no new table or migration. Two agent operations: `site.logs.read` and `logs.retention.set`.
- Changes in several places: nginx template (per-site `access_log`/`error_log`), PHP pool and unit (`LogsDirectory=`), installer (logrotate file, package), agent (directory lifecycle, new operation), authz table, API, UI, wiki page.
- Existing sites get their new log files on the first `web.apply` after the update; nothing is moved from the old server-wide files.
- Disk: logs count against `/var/log`, not against the site's disk quota, because root owns them [A]. A busy site can fill `/var/log` within the retention; the doctor check for free space covers `/` today [A: check].
- Tests: agent unit tests for cursor, rotation, truncation, symlinks, wrong owner, unknown kind, long lines and binary data; nginx and PHP template tests; authz tests for a user without a grant; the container test checks that a request lands in the site's access log and a PHP warning in the PHP log; an e2e screenshot test of the tab.
- Risk: the site's own code decides what it writes to the PHP log, including data of its visitors. That is the site owner's responsibility, as today in the journal.
- Not in scope for v1: older rotated files in the panel, search on the server, log download of whole files, app logs of Node.js and Python sites (they follow with those site types), traffic statistics from the access logs (P2).

## Open questions for Kim

1. ~~Keep logs 14 days?~~ Decided (Kim, 2026-11-21): the retention is set in the panel; default 7 days, range 1 to 365 (recommended in this ADR).
