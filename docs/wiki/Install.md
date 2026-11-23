# Install

**For:** server admin (needs root on the server).

Hostashi installs on a **fresh** server and sets up everything it needs itself: nginx, PHP-FPM, MariaDB, fail2ban, a firewall, disk quota and the panel. Do not install it on a server that already hosts websites.

## Requirements

| | |
|---|---|
| Operating system | Debian 13 or Ubuntu 26.04 LTS, freshly installed |
| Architecture | amd64 or arm64 |
| Root file system | ext4, if you want disk limits per site (other file systems work without them) |
| Access | SSH as root, or a user with `sudo` |
| Network | ports 22, 80, 443 and the panel port (8443 by default) reachable; see [Ports and files](Ports-and-files.md) |

The installer refuses to run if it finds another web server (Apache, Caddy, lighttpd) or MySQL. Nothing is changed in that case.

## Before you start: the panel address

Pick a host name for the panel, such as `panel.example.com`, and create a DNS `A` record (and `AAAA` for IPv6) that points to the server. Hostashi gets a Let's Encrypt certificate for that name during the install, so the record must already resolve.

Without a host name the panel only listens on the server itself (`127.0.0.1:8080`), and you reach it through an SSH tunnel.

## 1. See what would change

Download the install script, read it, and run it without `--yes` first. It then only shows its plan:

```sh
curl -fsSLO https://get.hostashi.com/install.sh
less install.sh
sudo bash install.sh --panel-domain panel.example.com
```

The script adds the Hostashi apt repository (`get.hostashi.com/apt`), trusts its signing key for that repository only, installs the `hostashi` package and runs the installer that comes with it. It accepts every [installer option](#installer-options).

## 2. Install

```sh
sudo bash install.sh --yes --panel-domain panel.example.com
```

At the end it prints a **one-time setup token**, valid for 24 hours. Keep it for the first login.

Running it again is safe: the installer only changes what is not set up yet, and says `Nothing to do.` when everything is in place. If a step fails, fix the cause and run the same command again. Later updates come with `sudo apt upgrade`; see [Updates with the package](#updates-with-the-package).

### Without the apt repository

To install a binary you built yourself (see [CONTRIBUTING.md](https://github.com/maki-it/hostashi/blob/main/CONTRIBUTING.md)), copy it to the server together with the repository's `packaging/` folder and run the installer directly. `plan` shows what would change, `apply --yes` does it:

```sh
sudo ./packaging/install.sh plan --binary ./hostashi --panel-domain panel.example.com
sudo ./packaging/install.sh apply --yes --binary ./hostashi --panel-domain panel.example.com
```

### Reboot for disk quota

On the first run the installer prepares disk quota and asks for **one reboot**:

```text
REBOOT REQUIRED: disk quota is prepared and becomes active after one reboot.
```

It never reboots by itself. Reboot when it suits you, then run the installer again to check that quota is active.

If `/home` is a file system of its own (a second disk or volume), the installer leaves quota to you and says so at the end. Enable project quota on it as described for [storage pools](Command-line.md#storage-pools) (ext4: `tune2fs -O project,quota -Q prjquota` while it is unmounted, then `prjquota` in `/etc/fstab`); the agent enforces the limits once it is mounted with it.

## 3. First login

At the end the installer prints a one-time link that contains the setup token:

```text
First login: open this link to create the administrator (one-time, valid 24 hours):
  https://panel.example.com:8443/#/setup/3f9c…
```

Open it, choose a user name and a password and create the first admin account. The token is after the `#`, so the browser never sends it to the server, and the panel removes it from the address bar. If you lost the token, print it again on the server and open `https://panel.example.com:8443/#/setup/<token>` or type it in:

```sh
sudo hostashi setup-token
```

## Panel without a port

To open the panel as `https://cp.example.com/` instead of `https://cp.example.com:8443/`, install with port 443:

```sh
sudo bash install.sh --yes --panel-domain cp.example.com --panel-port 443
```

Before that, create a DNS record for the name that points to the server (`A`, and `AAAA` for IPv6), so that Let's Encrypt can check it. nginx then serves the panel on port 443 next to the sites and tells them apart by the host name. That name cannot be used for a site or a parked domain afterwards.

To switch an installed server between 8443 and 443, run the installer again with the other `--panel-port`. The installer changes the settings and the firewall and restarts the panel.

Things to know:

- The panel allowlist (Firewall page) is applied by nginx on port 443, not by the firewall. Addresses outside it still reach the TLS handshake and then get `403`. On port 8443 the firewall drops them earlier.
- If fail2ban bans an address for wrong panel logins, it cannot reach the sites on this server either while the ban lasts. That also happens on 8443.
- The database tool (Adminer) keeps its own port 8444, so it stays a separate origin from the panel.

## Installer options

| Option | Meaning |
|--------|---------|
| `--binary PATH` | The `hostashi` binary to install. Required for `apply`. |
| `--panel-domain NAME` | The host name users open the panel at, such as `panel.example.com`, without `https://` or a port. nginx serves the panel there over HTTPS on the panel port with a Let's Encrypt certificate, so the name needs a DNS record pointing to the server. Needed for passkeys. Without it, the panel is reachable only through an SSH tunnel. |
| `--panel-port PORT` | HTTPS port of the panel, default `8443`. TCP and UDP must be open. `443` serves the panel on the sites' port under its own host name and needs `--panel-domain`. See [Panel without a port](#panel-without-a-port). |
| `--listen ADDR` | Where the panel process itself listens, default `127.0.0.1:8080`. |
| `--php-versions LIST` | More PHP versions from sury.org next to the distribution's, comma-separated, such as `8.3,7.4`. 7.4, 8.1 and 8.2 are legacy versions. |
| `--firewall-allow PORT/PROTO` | Also open this port to the internet, such as `3000/tcp`. Can be given more than once. |
| `--no-firewall` | No Hostashi firewall. Only use this behind a provider firewall. |
| `--no-db-tool` | Do not install the web database tool (Adminer on port 8444). Given on a server that has it, the installer removes it. |
| `--no-quota` | Do not enable disk quota. Sites then have no disk limit. |
| `--auto-update` | Also install new Hostashi versions automatically: unattended-upgrades takes them from `get.hostashi.com/apt` together with the daily security updates. Off by default; then `sudo apt upgrade` installs them. Each update keeps a copy of the panel database first. A later `apply` without the option turns it off again. |
| `--allow-untested` | Accept an operating system release that is not on the supported list. |
| `--no-apt` | Install no packages. A step that needs a missing package fails and names it. Meant for runs from the `hostashi` package, which brings its dependencies. |

### Saved options and `upgrade`

After each successful `apply` the installer saves its options (all of the above except `--binary` and `--no-apt`) to `/etc/hostashi/install.options`, one per line, owned by root with mode `0600`. `install.sh upgrade` runs `apply --yes --no-apt` again with these options and the binary of the `hostashi` package (`/usr/bin/hostashi`, or `--binary PATH`). To change an option, run `apply` again with the new options; the file follows.

The installer refuses an options file that anyone but root could have written, or that holds anything but the options above.

### Updates with the package

The Health page and the overview show when a newer version is in the Hostashi repository (from apt's package list, which apt refreshes every day), and admins get one email per new version. On a server set up this way, installing a newer `hostashi` package (`apt upgrade`) updates it by itself. Before the new files land, the package stops the panel and keeps a copy of the panel database in `/var/lib/hostashi-installer/upgrade/` (the three newest). Then it runs `install.sh upgrade`, which restarts agent and panel, and waits up to 60 seconds for the panel to answer. The output goes to `/var/log/hostashi-upgrade.log`.

If that fails, the panel runs again as far as possible and apt reports an error; the package stays unconfigured. Fix the cause (the log says what failed), then run `sudo dpkg --configure -a`.

### Going back to an older version

If the older version uses the same database schema, apt goes back by itself: `sudo apt install --allow-downgrades hostashi=VERSION`. If the schema differs, the older panel could not open the database, so the package stops the downgrade before anything changes and names the way back:

```sh
sudo hostashi-rollback VERSION
```

It restores the database copy that the package kept when it upgraded from `VERSION` and installs that version. Changes made in the panel since then are lost; sites, their files and databases are not touched. The current database stays next to the copies as `before-rollback-<version>.db`. Without `VERSION` it takes the newest older version with a copy; `--deb FILE` installs from a downloaded package instead of apt, `--yes` skips the question.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Done, or nothing to do. |
| `2` | Refused by the checks before anything changed. The server is unchanged. |
| `3` | A step failed. Fix the cause and run the installer again. |
| `64` | Wrong options. |

The installer writes a log to `/var/log/hostashi-install.log`.
