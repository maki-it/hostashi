# Ports and files

**For:** server admin.

## Ports

| Port | Protocol | What | Open to |
|------|----------|------|---------|
| 22 | TCP | SSH and SFTP for you and for site users | internet; can be limited to an allowlist in the panel |
| 80 | TCP | websites over HTTP, Let's Encrypt checks, redirects to HTTPS | internet, always |
| 443 | TCP and UDP | websites over HTTPS and HTTP/3 | internet, always |
| 8443 | TCP and UDP | the panel (the port can be changed with `--panel-port`) | internet; can be limited to an allowlist in the panel |

With `--panel-port 443`, the panel has no port of its own: nginx serves it on 443 under its host name, next to the sites. The firewall cannot tell panel and site traffic apart on a shared port.

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
| `/var/log/nginx/sites/<site>/` | access and error logs of a site |
| `/var/log/hostashi-install.log` | the installer's log |

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
| `hostashi-backup.timer` | starts the scheduled backups |

Check a service with `systemctl status <unit>` and its log with `journalctl -u <unit>`.
