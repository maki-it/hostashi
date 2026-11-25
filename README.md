# Hostashi

A modern, **truly open source** hosting control panel for single Linux servers, as an alternative to [CloudPanel](https://www.cloudpanel.io/).

[![Latest release](https://img.shields.io/github/v/release/maki-it/hostashi?sort=semver&label=release)](https://github.com/maki-it/hostashi/releases/latest)
[![License: AGPL-3.0-or-later](https://img.shields.io/badge/license-AGPL--3.0--or--later-blue)](LICENSE)

> **Status:** early releases, before 1.0. The [latest release](https://github.com/maki-it/hostashi/releases/latest) installs from a signed apt repository at `get.hostashi.com/apt`; updates come through apt, automatically if you choose `--auto-update`. Hostashi is used on its maintainer's own servers first; expect changes before 1.0. Install it on a **fresh** Debian 13 or Ubuntu 26.04 server (amd64 or arm64) only.

## What it does

- **Sites:** PHP (several versions side by side), Node.js, Python, static, reverse proxy and redirect sites on nginx, plus app sites that run a program of their own behind nginx. A site's type and its domain can be changed later, with redirect domains and a choice between `www` and the bare domain. Sites that run code get their own Linux user, SSH/SFTP access, cron jobs, resource limits, usage graphs and logs; Node.js, Python and app sites also get environment variables.
- **Deploy and files:** deploy from a Git repository, with a web file manager and a button to reset file permissions.
- **Performance:** FastCGI page cache and a Valkey object cache per site, PHP settings per site.
- **HTTPS and access:** Let's Encrypt certificates for every domain, or your own certificate; HSTS, security headers, password protection (basic auth) and IP rules per site; the visitor's real address behind a proxy or CDN.
- **Databases:** MariaDB on the server, plus external MariaDB, MySQL and PostgreSQL servers; size and connection limits, import, export and a web database tool.
- **Backups:** daily restic backups of files and databases to any SFTP server (for example a Hetzner Storage Box), with restore from the panel.
- **Server:** storage pools on extra volumes, firewall that rolls back unconfirmed changes, fail2ban, virus scan, services and dashboard, time zone and host name, e-mail notifications, audit log.
- **Users and roles:** admin, manager and user, with a second factor (TOTP or passkeys), and API tokens with an IP allowlist.
- **Import from CloudPanel:** scan the old server, plan, transfer sites and databases, sync once more before you move DNS.
- **API and command line:** a REST API under `/api/v1` described in [`api/openapi.json`](api/openapi.json). The `hostashi` command uses the same API after `hostashi login` and can run every API operation.

Not in scope for 1.0: mail server, DNS server, billing, resellers and more than one server.

## Install

```sh
curl -fsSLO https://get.hostashi.com/install.sh
less install.sh
sudo bash install.sh --panel-domain panel.example.com        # shows the plan
sudo bash install.sh --yes --panel-domain panel.example.com  # installs
```

The script adds the signed Hostashi apt repository (`get.hostashi.com/apt`), installs the `hostashi` package and runs its installer. Later updates come with `sudo apt upgrade`. Releases are signed with Sigstore; see [Releases](https://github.com/maki-it/hostashi/wiki/Releases) for how to check a download.

Requirements, options and the first login: [Install](https://github.com/maki-it/hostashi/wiki/Install) in the wiki.

## Documentation

- **Using Hostashi:** the [wiki](https://github.com/maki-it/hostashi/wiki). It is written in [`docs/wiki/`](docs/wiki/) and mirrored after each merge ([ADR-0043](docs/adr/0043-user-documentation.md)).
- **How it is built:** the planning documents below and the architecture decision records in [`docs/adr/`](docs/adr/).

| # | Document | Content |
|---|----------|---------|
| 1 | [CloudPanel research](docs/01-cloudpanel-research.md) | What CloudPanel can do, legal situation, weaknesses |
| 2 | [Vision & scope](docs/02-vision-scope.md) | Target audience, positioning, non-goals |
| 3 | [Feature matrix](docs/03-features.md) | Features with priority and phase |
| 4 | [Architecture](docs/04-architecture.md) | Components, privilege separation, reconciler, data model, installer |
| 5 | [Security](docs/05-security.md) | Threat model and mandatory security requirements |
| 6 | [Roadmap](docs/06-roadmap.md) | Phases, milestones, acceptance criteria, test strategy |
| 7 | [Decisions](docs/07-decisions.md) | Index of all ADRs |

## How it works

One Go binary with two roles: `hostashi serve` runs the panel and API without root, and `hostashi agent` runs as root and only accepts a fixed set of typed operations, with no shell strings. State lives in SQLite; nginx, PHP-FPM and systemd configurations are generated from it, checked and rolled back on failure. The web UI is React and is embedded in the binary.

## Contributing and security

Layout, local checks and rules: [`CONTRIBUTING.md`](CONTRIBUTING.md). Report security problems as described in [`SECURITY.md`](SECURITY.md), not in a public issue.

## License

Code: **AGPL-3.0-or-later** ([`LICENSE`](LICENSE)). Documentation: **CC-BY-4.0** ([`LICENSES/CC-BY-4.0.txt`](LICENSES/CC-BY-4.0.txt)). Contributions with DCO sign-off, no CLA (see [`CONTRIBUTING.md`](CONTRIBUTING.md)). The name and the logo are not covered by the code license.
