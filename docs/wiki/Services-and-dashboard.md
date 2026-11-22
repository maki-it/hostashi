# Services and dashboard

Hostashi shows the system services the panel depends on, restarts them on request and keeps a history of the server's load: CPU, memory, network and disks.

**For:** admins. Managers and users see neither the services nor the dashboard.

## Dashboard

The **Overview** shows the card **Server load** below the site and health cards.

- Pick the range at the top right: **1h**, **24h**, **7d** or **30d**.
- **CPU** shows the busy share of all cores and the share spent waiting for the disk.
- **Memory** shows memory in use out of the server's total.
- **Network** shows incoming and outgoing traffic of all interfaces together, in bits per second.
- **Disks** shows each file system and the swap as they are now. A bar turns orange at 80 % and red at 90 %.
- Point at a chart, or tap it on a phone, to see the values at that time. **Show as table** lists every value.

The panel takes a sample every 15 seconds. The last hour is kept in memory; 5-minute averages are kept for 48 hours and hourly averages for 30 days. After a restart of the panel the last hour starts empty again.

## Services

Open **Services** in the Server part of the menu. The table lists each service with its state, how long it has been running, its memory and how often systemd restarted it after a crash in the last 24 hours.

| Service | What you can do |
|---------|-----------------|
| Web server (nginx) | Start, restart, reload. Reload and restart first check the configuration. |
| MariaDB | Start, stop, restart. Stopping asks you to type `stop mariadb`, because every site with a local database stops working. |
| SSH server | Reload only, after checking its configuration. |
| Brute-force protection (fail2ban) | Start, stop, restart, reload. |
| Firewall | Reload only. |
| Database tool (Adminer) | Start, stop, restart. |
| Panel, Agent | Nothing: Hostashi manages them itself. |
| PHP-FPM of a site | Start, restart, reload. |
| Object cache (Valkey) of a site | Start, restart. |

Stopping a service always asks first. A site's own PHP and Valkey services are also shown on the site's **Overview** tab.

**Recent events** below the table lists the last 30 days: automatic restarts after a crash, failures, a failed service running again, and every action someone took, with the account's name. Every action is also written to the [audit log](Audit-log.md).

With email set up, Hostashi also sends an email when a service is down or keeps crashing, and again when it runs; see [Email notifications](Email-notifications.md).

nginx and MariaDB are restarted by systemd 5 seconds after a crash. The installer sets this up.

## API

API tokens use the scopes `services:read` and `services:write`, only for admins:

- `GET /api/v1/dashboard?range=1h` (or `24h`, `7d`, `30d`) returns the points and the step between them in seconds.
- `GET /api/v1/services` and `GET /api/v1/sites/NAME/services` list the services.
- `GET /api/v1/services/events` with the optional query values `service`, `site` and `limit`.
- `POST /api/v1/services/ID/ACTION` and `POST /api/v1/sites/NAME/services/ID/ACTION` run an action. Stopping MariaDB needs the body `{"confirm": "stop mariadb"}`.

## Command line

See [Command line](Command-line.md#services).
