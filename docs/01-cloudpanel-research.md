# 1. Research: CloudPanel

> **Planning document.** Written in November 2026 during the planning phase, before the first release, and kept as the record of why Hostashi is built the way it is. Where it differs from the product, the [ADRs](adr/) and the [user documentation](wiki/Home.md) are current.

Research as of 2026-11-17. It is based on the five public CloudPanel repositories (shallow-cloned and read). I did **not** evaluate the GitHub issues, the API was not enabled in that session. This can be done later.

## 1.1 What the repositories actually contain

| Repo | Content | License (checked) | Consequence for us |
|------|---------|-------------------|--------------------|
| `cloudpanel-ce` | Only `README.md`, images and `.github`. **No source code.** | none | There is nothing to fork. CloudPanel is "free" but not open source in the OSI sense. |
| `vhost-templates` | Nginx vhost templates (v1, v2, v2-http3, v2-varnish) for about 30 applications | **no LICENSE file** | Legally: all rights reserved. Read as a reference, **do not copy**. |
| `docs` | Docusaurus documentation (v1, v2), around 2000 Markdown files | **no LICENSE file** | Good functional reference, do not reuse the texts. |
| `dploy` | CLI for zero-downtime deployment (PHP/Symfony, Composer project) | **MIT** (copyright Anton Medvedev, so probably a Deployer fork, please check) | Reusable or usable as a model. |
| `clp-wp-varnish-cache` | WordPress plugin for Varnish purge | **GPLv3** (according to `readme.txt`) | Only usable if our project has a GPL-compatible license. |

The license information comes from the files in the clones. I could not cross-check it through the GitHub API.

**Consequence:** We build *clean-room*. CloudPanel serves as a functional reference (what it can do, how the workflow feels), but we write templates, texts and structures ourselves. The name "CloudPanel" may only appear descriptively in comparisons, never in the product name. See ADR-0001.

## 1.2 Feature set of CloudPanel v2 (from the documentation)

**Platform**

- Ubuntu 22.04/24.04/26.04 and Debian 11/12/13, x86-64 and ARM64, at least 1 core, 2 GB RAM, 10 GB disk.
- VMs only, **no containers** (LXC, LXD, OpenVZ).
- Stack: Nginx 1.30 (HTTP/3, PageSpeed), MySQL 8.0/8.4, MariaDB 10.6–12.3, PHP 7.1–8.5, Redis, ProFTPD, Node.js 12–24 (nvm), Python 3.10–3.14, Varnish 7.x.
- Installation by script or marketplace image on AWS, DigitalOcean, Hetzner, GCE, Azure, Oracle, Vultr.

**Sites** (frontend area)

- Types: WordPress, PHP (with vhost template), Node.js, static HTML, Python, reverse proxy.
- One SSH user per site ("site user") with its home under `/home/<user>`.
- Automatic www/non-www redirect, HTTP→HTTPS.
- Vhost editor with syntax check and automatic revert on errors, HTTP/3 switchable per site.
- TLS: Let's Encrypt, Cloudflare origin certificate, import of custom certificates.
- Databases: create/delete, DB users, phpMyAdmin, export/import, remote access, multiple DB servers, master credentials.
- File manager, SSH/SFTP/FTP users, cron jobs, logs.
- Security per site: IP blocking, bot blocking, basic auth, Cloudflare IP handling.
- PageSpeed module, Varnish cache with WordPress plugin, Redis.

**Admin area**

- Firewall based on UFW, basic auth in front of the panel, panel port 8443.
- Users with three roles: admin, site manager, user (restricted to sites). 2FA (TOTP).
- Events (audit log), instance management (reboot, restart services, time zone, ProFTPD), custom domain for the panel.
- Remote backups via Rclone: S3, Wasabi, DO Spaces, Dropbox, Google Drive, Hetzner Storage Box, SFTP, custom Rclone config. Cloud features (for example snapshots at Hetzner).
- Multilingual UI (including Finnish, Hungarian, Slovak, Thai).

**CLI `clpctl`**

- Root: create/delete sites (PHP, Node, static, Python, reverse proxy), DBs, Let's Encrypt, users, vhost templates, basic auth, Cloudflare IPs.
- Site user: reset permissions, DB export/import, Varnish purge.

**Not included:** mail server (the documentation points to Mailcow and external SMTP providers), DNS server, multi-server management, reseller features, PostgreSQL, container apps.

## 1.3 Release cadence

v2.5.0 (11/2024), 2.5.1 (03/2025), 2.5.2 (08/2025), 2.5.3 (12/2025), 2.5.4 (07/2026): roughly every 3–7 months. The content is mostly new OS/PHP/DB versions and bug fixes. There are hardly any new features. That is the gap we can fill.

## 1.4 Observed weaknesses

Derived from the changelog and vhost template, not an assessment of CloudPanel as a whole:

- **Input validation and file operations:** A folder named "0" caused the parent directory to be deleted on removal (#542). Cron rejected commands containing the word "admin" (#582). This points to filters instead of typing.
- **Permissions:** Wrong file permissions on new sites (#558). Deleting a site left the user crontab behind (#758). A PHP-FPM socket misconfiguration was reported externally as a security vulnerability (2.5.2). Several releases contain externally reported security fixes.
- **PHP-FPM over TCP:** The generic template uses `fastcgi_pass 127.0.0.1:<port>` and sets `PHP_VALUE` from the Nginx configuration. Unix sockets with owner permissions and pool configuration would be cleaner.
- **Closed core:** Bugs and security vulnerabilities can only be fixed by the vendor, the community cannot review.
- **No containers, no Postgres, no multi-server, no staging.**

## 1.5 Market landscape (from memory, not researched)

Comparable projects to look at before fixing the scope: HestiaCP, ISPConfig, Virtualmin, 1Panel, Coolify, Dokploy, CapRover, and commercially Plesk, cPanel, RunCloud, SpinupWP, Ploi, Enhance. A real market analysis (stars, activity, license, target audience, weaknesses) is a task in Phase 0.
