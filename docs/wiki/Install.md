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

## 1. Get Hostashi

There is no release yet. Until there is, build the `hostashi` binary from the repository as described in [CONTRIBUTING.md](https://github.com/maki-it/hostashi/blob/main/CONTRIBUTING.md), and copy it to the server together with the `packaging/` folder.

## 2. See what would change

The installer has two modes. `plan` only reads and lists what it would do:

```sh
sudo ./packaging/install.sh plan --binary ./hostashi --public-url https://panel.example.com
```

## 3. Install

```sh
sudo ./packaging/install.sh apply --yes --binary ./hostashi --public-url https://panel.example.com
```

At the end it prints a **one-time setup token**, valid for 24 hours. Keep it for the first login.

Running the installer again is safe: it only changes what is not set up yet, and says `Nothing to do.` when everything is in place. If a step fails, fix the cause and run the same command again.

### Reboot for disk quota

On the first run the installer prepares disk quota and asks for **one reboot**:

```text
REBOOT REQUIRED: disk quota is prepared and becomes active after one reboot.
```

It never reboots by itself. Reboot when it suits you, then run the same `apply` command again to check that quota is active.

## 4. First login

Open the panel address (for example `https://panel.example.com:8443`), enter the setup token and create the first admin account. If you lost the token, print it again on the server:

```sh
sudo hostashi setup-token
```

## Installer options

| Option | Meaning |
|--------|---------|
| `--binary PATH` | The `hostashi` binary to install. Required for `apply`. |
| `--public-url URL` | The address users open the panel at, such as `https://panel.example.com`. With a host name, nginx serves the panel over HTTPS on the panel port with a Let's Encrypt certificate. Needed for passkeys. |
| `--panel-port PORT` | HTTPS port of the panel, default `8443`. TCP and UDP must be open. |
| `--listen ADDR` | Where the panel process itself listens, default `127.0.0.1:8080`. |
| `--php-versions LIST` | More PHP versions from sury.org next to the distribution's, comma-separated, such as `8.3,7.4`. 7.4, 8.1 and 8.2 are legacy versions. |
| `--firewall-allow PORT/PROTO` | Also open this port to the internet, such as `3000/tcp`. Can be given more than once. |
| `--no-firewall` | No Hostashi firewall. Only use this behind a provider firewall. |
| `--no-quota` | Do not enable disk quota. Sites then have no disk limit. |
| `--allow-untested` | Accept an operating system release that is not on the supported list. |

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Done, or nothing to do. |
| `2` | Refused by the checks before anything changed. The server is unchanged. |
| `3` | A step failed. Fix the cause and run the installer again. |
| `64` | Wrong options. |

The installer writes a log to `/var/log/hostashi-install.log`.
