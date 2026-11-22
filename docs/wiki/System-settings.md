# System settings

Hostashi shows the server's hostname, time zone, clock and uptime, changes the hostname and the time zone, and reboots the server ([ADR-0060](https://github.com/maki-it/hostashi/blob/main/docs/adr/0060-time-zone-hostname-and-reboot.md)).

**For:** admins. Managers and users do not see the page.

Open **System** in the Server part of the menu.

## Server

The first card shows the hostname, the time zone, the server's clock and whether it is synchronized, and the uptime. **Reboot needed** appears when installed updates wait for a reboot, with the packages that asked for it. The server never reboots by itself.

## Hostname

Use lowercase letters, digits and hyphens. A full name such as `web2.example.org` works too, up to 64 characters. The panel's address and the sites do not change.

Hostashi sets the name through systemd and also:

- points the `127.0.1.1` line in `/etc/hosts` to the new name, so `sudo` and local lookups keep working;
- writes `/etc/cloud/cloud.cfg.d/90-hostashi-hostname.cfg` if cloud-init is installed, so it does not set the cloud's server name again at the next boot.

## Time zone

Type to search the list of zones the server knows. Before you save, the hint shows what the server's clock would read.

- The sites' cron jobs follow the new time zone at once.
- MariaDB uses it after its next restart (**Services**, MariaDB, restart).
- PHP keeps its own `date.timezone` (see [PHP settings](PHP-settings.md)).

## Reboot

**Reboot server…** asks you to type the hostname. Every site, the databases and the panel stop until the server is back, usually within one or two minutes. The page waits and reconnects by itself. If the panel does not answer for ten minutes, check the server in your hosting provider's console.

## Audit log

Each change is in the [audit log](Audit-log.md): `system.hostname` and `system.timezone` with the old and the new value, `system.reboot` with the hostname and whether updates waited for it.

## API and command line

| Route | Scope |
|-------|-------|
| `GET /api/v1/system`, `GET /api/v1/system/timezones` | `system:read` |
| `PUT /api/v1/system/hostname`, `PUT /api/v1/system/timezone` | `system:write` |
| `POST /api/v1/system/reboot` with `{"confirm": "<hostname>"}` | `system:write` |

On the server, as root: `hostashi system info`, `hostashi system timezones`, `hostashi system timezone ZONE`, `hostashi system hostname NAME` and `hostashi system reboot HOSTNAME` (see [Command line](Command-line.md)).

## Requirements

The agent talks to systemd-timedated, systemd-hostnamed and systemd-logind over the system bus. The installer adds the `dbus` and `tzdata` packages. Without a running bus the page says so and changes nothing.
