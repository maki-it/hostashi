# Ports and files

**For:** server admin.

## Ports

| Port | Protocol | What | Open to |
|------|----------|------|---------|
| 22 | TCP | SSH and SFTP for you and for site users | internet; can be limited to an allowlist in the panel |
| 80 | TCP | websites over HTTP, Let's Encrypt checks, redirects to HTTPS | internet, always |
| 443 | TCP and UDP | websites over HTTPS and HTTP/3 | internet, always |
| 8443 | TCP and UDP | the panel (the port can be changed with `--panel-port`) | internet; can be limited to an allowlist in the panel |
| 8444 | TCP | the database tool (Adminer), opened from the panel; not there with `--no-db-tool` | internet; the panel's allowlist applies |

With `--panel-port 443`, the panel has no port of its own: nginx serves it on 443 under its host name, next to the sites. The firewall cannot tell panel and site traffic apart on a shared port, so nginx applies the panel allowlist there instead.

Hostashi's firewall lets in only these ports, plus what you open in the panel or with `--firewall-allow`. Outgoing traffic is not limited. If you have a firewall at your provider as well, open the same ports there.

## Files and directories

| Path | What |
|------|------|
| `/usr/local/bin/hostashi` | the program (panel, agent and command line in one file) |
| `/etc/hostashi/hostashi.env` | settings written by the installer, such as the panel address |
| `/etc/hostashi/secret.key` | the panel's secret key. **Back it up outside the server**; see below. |
| `/var/lib/hostashi/hostashi.db` | the panel database: sites, users, settings |
| `/home/<site>/htdocs` | the web root of a site; `/home/<site>` is the home of the site's Linux user |
| `/home/.hostashi-cache/<site>` | the page cache of a site, if it is on. Only the web server can read it; it counts against the site's disk limit and is not backed up. |
| `/run/hostashi-valkey/<site>/valkey.sock` | the object cache (Valkey) of a site, if it is on. Only the site's own user can open it; it holds nothing worth keeping and is empty after a restart. |
| `/var/log/nginx/sites/<site>/` | access and error logs of a site, readable only by root and in the panel |
| `/var/log/hostashi/php/<site>/` | PHP error log of a site, readable only by root and in the panel |
| `/var/log/nginx/hostashi/` | access and error logs of requests for no site |
| `/etc/logrotate.d/hostashi` | daily rotation of these logs; the panel sets how many days are kept (default 7) |
| `/var/log/hostashi-install.log` | the installer's log |
| `/usr/share/hostashi/adminer/` | the database tool: the pinned Adminer (its build with all drivers; Hostashi's front lets only the MySQL and PostgreSQL drivers run), Hostashi's front for it, and `adminer.css` with the panel's fonts for the panel's look |
| `/run/hostashi-adminer/tickets/` | one-time tickets for the database tool; each is removed on first use or after 60 seconds |
| `/run/hostashi-adminer/sessions/` | when each open session of the database tool ends, written by the agent when a session is opened, extended or ended in the panel |
| `/etc/hostashi-adminer/servers/` | the external database servers the database tool may reach: their addresses, a hosts file and their CA certificates, written by the agent |

### Keys to keep outside the server

Backups are encrypted. Without the repository password and the panel key you cannot restore them on a new server. Print both once and store them in a password manager:

```sh
sudo hostashi backup key show
```

## Services

| Unit | What |
|------|------|
| `hostashi.service` | the panel (runs without root) |
| `hostashi-agent.service` | the agent that makes changes on the system (runs as root, only fixed operations) |
| `hostashi-php-<version>@<site>.service` | the PHP-FPM process of one site |
| `hostashi-files@<site>.socket`, `hostashi-files@<site>.service` | the file manager of one site: started by its socket when the Files tab is used, runs as the site's user, stops after 5 minutes without requests |
| `hostashi-backup.timer` | starts the scheduled backups |
| `hostashi-adminer.service` | the PHP-FPM process of the database tool (runs as `hostashi-adminer`; network only to the external database servers, through the drop-in `hostashi-adminer.service.d/servers.conf`) |

Check a service with `systemctl status <unit>` and its log with `journalctl -u <unit>`.
