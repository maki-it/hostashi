# ADR-0052 Service management and server dashboard

**Status:** Accepted (2026-11-21, by Kim). **Date drafted:** 2026-11-21. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0015 (per-site resource limits), ADR-0022 (metrics), ADR-0033 (system hardening), ADR-0035 (users and roles), ADR-0045 (object cache per site), [`03-features.md`](../03-features.md) section 3.0 and rows "Manage services" and "Dashboard with CPU/RAM/disk/network" (both P0), [`04-architecture.md`](../04-architecture.md) section 4.7.

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Both rows are P0 and part of release 1.0 (PR #315) [V: `03-features.md` 3.0]. The feature row says: "Nginx, PHP-FPM, DB, Redis. systemd restarts crashed services automatically (`Restart=`), the panel shows restarts". The dashboard row says: "Local ring buffer, no external TSDB" [V].
- `04-architecture.md` 4.7 plans the ring buffer in the panel, for example 30 days with tiered resolution, and says Prometheus or Influx must not be required [V]. ADR-0022's `/metrics` is an optional export and does not replace it [V].
- The panel runs as `hostashi` with `ProcSubset=pid` and `ProtectProc=invisible` [V: `packaging/systemd/hostashi.service`]. It cannot read `/proc/stat`, `/proc/meminfo` or `/proc/net/dev`, and it has no way to talk to systemd [V: unit file; A: not tried in the unit, follows from `ProcSubset=pid`]. Host numbers and unit states must come from the agent.
- The agent already runs `/usr/bin/systemctl` through `internal/phpctl`, an `os/exec` exception with fixed argument lists and unit names checked against fixed patterns or a fixed list [V: `internal/phpctl/phpctl.go`, `system.go`]. `systemctl show --property=... --value` is used there to read states [V].
- Units on a Hostashi server [V: `packaging/`, `internal/`]:
  - From the distribution: `nginx.service`, `mariadb.service`, `ssh.service`, `fail2ban.service`.
  - Hostashi's own: `hostashi.service` (panel), `hostashi-agent.service`, `hostashi-firewall.service` (oneshot, loads nftables), `hostashi-adminer.service`.
  - Per site: `hostashi-php-<version>@<site>.service` (one FPM pool per site, there is no shared PHP-FPM service), `hostashi-valkey@<site>.service` (object cache, ADR-0045).
- `Restart=on-failure` is set on Hostashi's own long-running units and the per-site PHP and Valkey units [V: grep over `packaging/`]. nginx and MariaDB use the distribution's units; Debian's `nginx.service` has no `Restart=` and MariaDB's has `Restart=on-abort` [U: from memory of the packages, check in the container test].
- systemd counts automatic restarts per unit in the property `NRestarts` [U]. A manual `systemctl restart` resets the counter [U: to verify in the container test]. So the counter alone cannot give a history; the panel has to note changes.
- Since the panel sits behind nginx on port 443 (ADR-0042), stopping nginx from the panel cuts the panel off; only SSH brings it back [V: ADR-0042; A: consequence].
- Roles: service actions are a server setting, so `admin` only (ADR-0035) [V]. The audit log is built in a parallel thread; service actions must write to it [A: interface not merged yet].

## Options

### Who talks to systemd

| Option | Pro | Contra |
|--------|-----|--------|
| **A. New typed agent operations, systemctl through `internal/phpctl` with a fixed catalog of units and actions** | No new `os/exec` exception, the same checks as today, one place that knows which unit may do what | phpctl's scope grows beyond PHP (it already covers firewall and cron units) |
| B. systemd over D-Bus (`github.com/coreos/go-systemd/dbus` or `godbus`) | No process start, structured properties | A new dependency and a socket into PID 1 with a much larger API than our five verbs [A]. The agent unit would need `AF_UNIX` to the system bus, it has it, but any bug could call any method [A] |
| C. A general `service.action(unit, verb)` operation | Flexible | Exactly the general exec-like interface rule 1 forbids |

### Where the dashboard numbers come from

| Option | Pro | Contra |
|--------|-----|--------|
| **D. Agent operation `host.stats` returns raw counters, the panel samples every 15 s [E] and keeps the ring buffer** | Agent stays stateless and read-only, rates are computed in one place, the panel already owns SQLite | One agent call every 15 s [E: cheap, reads five small files] |
| E. Agent samples and keeps the buffer itself | Survives a panel restart without SQLite | State in the root process, a second store to back up |
| F. Scrape our own `/metrics` or a node_exporter | Reuses Prometheus | `/metrics` is off by default, has no host series, and node_exporter is an extra service [V: ADR-0022] |

## Recommendation

**Option A and option D.**

### Service catalog

The catalog is a fixed table in `internal/agentapi`. The panel never sends a unit name; it sends a service ID (and a site name for per-site services), and the agent maps that to the unit.

| ID | Unit | Status | Start | Stop | Restart | Reload |
|----|------|--------|-------|------|---------|--------|
| `nginx` | `nginx.service` | yes | yes | **no** | yes, after `nginx -t` | yes, after `nginx -t` |
| `mariadb` | `mariadb.service` | yes | yes | yes, with confirmation | yes | no |
| `ssh` | `ssh.service` | yes | no | **no** | **no** | yes, after `sshd -t` |
| `fail2ban` | `fail2ban.service` | yes | yes | yes | yes | yes |
| `firewall` | `hostashi-firewall.service` | yes | no | **no** | no | yes (as today, ADR-0034) |
| `adminer` | `hostashi-adminer.service` | yes, if installed | yes | yes | yes | no |
| `panel` | `hostashi.service` | yes | no | no | no | no |
| `agent` | `hostashi-agent.service` | yes | no | no | no | no |
| `php` (per site) | `hostashi-php-<v>@<site>.service` | yes | yes | no (use disable site) | yes | yes |
| `valkey` (per site) | `hostashi-valkey@<site>.service` | yes, if the site has a cache | yes | no (turn the cache off) | yes | no |

- **No stop for nginx, SSH and the firewall** from the panel, because each one locks the admin out or opens the server. Restart of SSH is also left out; reload after `sshd -t` is enough to apply its config. Panel and agent are read-only: restarting them from the panel kills the request that asked for it.
- Stopping MariaDB needs a typed confirmation in the UI ("stop mariadb"), because every PHP site loses its database.
- Per-site PHP and Valkey are only started, restarted or reloaded. Turning them off stays with the existing switches (disable site, object cache off), which also change the stored target state; a stopped unit would otherwise be started again by the next reconcile [A].
- Whether a unit is installed is read from `LoadState`; a missing unit (Adminer off, fail2ban removed) is shown as "not installed" and has no actions.

### Agent operations

- `services.status` (read-only): states of all catalog units, or of one service. Fields per unit, from one `systemctl show` with a fixed property list: `LoadState`, `ActiveState`, `SubState`, `UnitFileState`, `NRestarts`, `ActiveEnterTimestamp`, `ExecMainStartTimestamp`, `MainPID`, `MemoryCurrent`, `Result`. The agent parses `key=value` lines and keeps only these keys; values are checked (enum, number, time), anything else is dropped.
- `services.action` (`service`, optional `site`, `action` from `start|stop|restart|reload`): the agent checks the action against the catalog row, runs the config test where the row says so, then `systemctl <verb> <unit>`. The answer is the new state from `services.status`.
- `host.stats` (read-only): raw counters from `/proc/stat` (CPU jiffies by kind, number of CPUs), `/proc/meminfo` (total, available, swap), `/proc/loadavg`, `/proc/uptime`, `statfs` of `/` and of each storage pool (ADR-0016), and `/proc/net/dev` bytes and packets per interface except `lo`, at most 16 interfaces whose names match `^[a-zA-Z0-9_.-]{1,15}$`. The agent has no network namespace of its own, so it sees the host's interfaces [V: `PrivateNetwork=no` in `hostashi-agent.service`].
- All three use the existing `internal/phpctl` exception (`systemctl show`, `systemctl <verb>`) plus `nginx -t` (internal/webctl, as today) and `sshd -t` (new fixed argument list in phpctl, `/usr/sbin/sshd -t`). No new package may start processes.

### Restarts

- systemd keeps restarting crashed units. Hostashi adds a drop-in with `Restart=on-failure`, `RestartSec=5s` and the default start limit to `nginx.service` and `mariadb.service` through the installer [A: the distribution's MariaDB unit may already restart; the drop-in only fills the gap].
- The panel polls `services.status` every 60 s [E] and stores an event in SQLite when `NRestarts` grows, `ExecMainStartTimestamp` changes or `ActiveState` becomes `failed`: time, service, site, kind (`crash-restart`, `started`, `failed`, `action` with the panel user). Migration 0043 adds the table `service_events`, kept for 30 days and at most 10,000 rows [E].
- The UI shows the last start, the number of automatic restarts in the last 24 hours and the event list. Panel actions also go into the audit log once it exists.

### Dashboard

- The panel calls `host.stats` every 15 s [E] and computes CPU % (busy, iowait, steal), RAM and swap use, load, disk use per mount and network bytes per second per interface.
- Ring buffer with three tiers [E]: 15 s points for 1 hour (240), 5 min averages for 48 hours (576), 1 h averages for 30 days (720). The 15 s tier stays in memory; the 5 min and 1 h tiers are written to SQLite (migration 0043, table `host_samples`) so a panel restart keeps the history. Roughly 1,300 rows of one small record [E: well under 1 MB].
- `GET /api/v1/dashboard?range=1h|24h|7d|30d` returns the series; `GET /api/v1/services` and `POST /api/v1/services/{id}/{action}` (and `/sites/{name}/services/{id}/{action}`) cover services. New scopes: `services:read` (status, events, dashboard) and `services:write` (actions, HighRisk). Both admin only, also through tokens.
- The overview page gets the graphs; a new page "Services" under server settings lists the catalog with state badges, the restart count, last start and the action buttons. A site's overview shows its PHP and Valkey state with restart and reload.
- Charts: a small SVG line chart of our own in `web/src/ui` [A: a library such as uPlot is 50 kB and MIT, but four charts do not need it; decide at implementation after checking the latest version].

## Consequences

- Three new agent operations, two new scopes, one migration (0043), one drop-in per distribution unit in the installer. No new `os/exec` exception; phpctl gains the catalog and `sshd -t`.
- Tests: catalog table test (every row, every refused action), parser tests for `systemctl show` output with odd values, `/proc` parser tests with fixtures from Debian 13 and Ubuntu 26.04, the restart detector (counter grows, counter resets after a manual restart, unit missing), tier aggregation, API tests for roles and scopes, and the container installer test for a real `restart` and `reload` of nginx and a crash restart of MariaDB (`kill -9`).
- Risk: an admin can still stop MariaDB and break every site until it is started again. Accepted as an admin action with confirmation [A].
- Not in scope: alerts and notifications on failures (P1 "Service health and disk warning"), per-site graphs of CPU and RAM (the per-site numbers stay in `/metrics`), journal logs of a service (thread "site logs"), managing other units.

## Open questions for Kim

None left.

1. ~~Is the catalog right, in particular no stop for nginx, SSH and the firewall, and stop for MariaDB only with a typed confirmation?~~ Decided (Kim, 2026-11-21): yes, as drafted.
2. ~~Drop-ins with `Restart=on-failure` for the distribution's nginx and MariaDB units: yes?~~ Decided (Kim, 2026-11-21): yes.
