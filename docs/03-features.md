# 3. Feature matrix

**Priority:** `P0` = no release without it, `P1` = needed for 1.0, `P2` = afterwards.
**Phase:** see [roadmap](06-roadmap.md). A = alpha (MVP), B = beta, 1 = release 1.0, 2+ = later.
**Source:** `CP` = parity with CloudPanel, `NEW` = differentiation.

## 3.1 Server and platform

| Feature | Prio | Phase | Source | Note |
|---------|------|-------|--------|------|
| Installer (idempotent, signed) and `.deb` package | P0 | A | CP | Ubuntu 22.04/24.04, Debian 12/13, amd64 + arm64 |
| Initial setup with a one-time token (no default credentials) | P0 | A | NEW | Prevents "first visitor becomes admin" |
| Manage services (start/stop/reload, status) | P0 | A | CP | Nginx, PHP-FPM, DB, Redis. systemd restarts crashed services automatically (`Restart=`), the panel shows restarts |
| Dashboard with CPU/RAM/disk/network | P0 | A | CP | Local ring buffer, no external TSDB |
| Firewall (nftables/UFW) with defaults | P0 | A | CP | SSH lockout protection (rollback timer) |
| Time zone, hostname, reboot | P1 | B | CP | |
| Panel update in the UI with preflight and rollback | P1 | B | NEW | Notice in the panel when an update is available; optional automatic updates (channel and time window selectable) |
| `hostashi doctor` (self-diagnosis) | P1 | B | NEW | |
| Operation on IPv6-only servers | P1 | B | NEW | Test panel, ACME, backups and package sources without IPv4 |
| UI usable on a phone | P1 | B | NEW | Responsive layout as a requirement for the SPA |
| Adopt mode (take over an existing server) | P1 | 1 | NEW | Basis for the import |
| Operation in LXC/Incus containers | P2 | 2+ | NEW | CloudPanel cannot do this |
| Multi-server (controller + agents) | P2 | 2+ | NEW | Prepare the architecture, do not build it |

## 3.2 Sites and runtimes

| Feature | Prio | Phase | Source | Note |
|---------|------|-------|--------|------|
| Site type static | P0 | A | CP | |
| Site type PHP (several versions in parallel, 7.4–8.6) | P0 | A | CP | FPM pool per site, Unix socket |
| Site type Node.js (systemd service, selectable versions) | P0 | A | CP | |
| Site type reverse proxy | P0 | A | CP | |
| Site type redirect (all requests to one target URL) | P1 | B | NEW | Without webroot and without site user |
| Site type Python (WSGI/ASGI, venv) | P1 | B | CP | |
| WordPress quick install (WP-CLI) | P1 | B | CP | |
| Linux user per site, home layout, permission reset | P0 | A | CP | |
| Change the domain of a site | P1 | B | NEW | A site is identified internally by a fixed ID, never by its domain (applies from alpha). Vhost, certificate and optionally DB contents are carried along |
| Disable a site, maintenance and parking pages | P1 | B | NEW | Store custom HTML pages once in the panel and enable them per site with a switch (UI, API, CLI), without an extra site and without manual vhost edits. Parking pages also for domains without a site of their own. Optional access for allowed IPs during maintenance |
| Aliases, redirects, www/non-www, HTTP→HTTPS | P0 | A | CP | |
| Vhost templates (Laravel, Symfony, WP, Magento, Shopware, …) | P1 | B | CP | Custom templates, YAML-based, preview |
| Vhost editor with `nginx -t` and rollback | P0 | A | CP | Custom snippets in include files |
| Per-site PHP settings (memory_limit, …) | P0 | A | CP | Through the pool config, not through `PHP_VALUE` in Nginx. PHP version visible in the site overview; unused PHP versions can be switched off |
| Resource limits per site (CPU, RAM, tasks, I/O) | P1 | B | NEW | systemd/cgroup v2 |
| Disk quota per site | P1 | B | NEW | Project quota (ext4/XFS), enabled during initial setup, details in [ADR-0015](adr/0015-per-site-resource-limits.md). Spike S8 done, ADR accepted. Used space per site (files and DB) is shown in the UI, even without quota |
| HTTP/3 per site | P1 | B | CP | |
| Docker Compose apps as a site type | P2 | 2+ | NEW | Evaluate rootless Podman as an option |
| Staging/cloning a site | P1 | 1 | NEW | Files + DB, domain rewrite |
| Zero-downtime deployments (releases, symlink, rollback) | P1 | 1 | CP (dploy) | Own implementation or dploy integration (MIT) |
| Git deploy via webhook | P2 | 2+ | NEW | |

## 3.3 TLS and domains

| Feature | Prio | Phase | Source | Note |
|---------|------|-------|--------|------|
| Let's Encrypt (HTTP-01), auto-renew | P0 | A | CP | Built-in ACME client (for example lego). New aliases and subdomains get a certificate automatically |
| Wildcard via DNS-01 | P1 | B | NEW | Providers: INWX, Cloudflare, Hetzner DNS, Route53 first |
| Import custom certificates | P0 | A | CP | |
| Cloudflare origin certificates and real IP handling | P1 | B | CP | |
| Panel under its own domain with a valid certificate | P0 | A | CP | |
| DNS record management through provider API | P2 | 2+ | NEW | |

## 3.4 Databases and cache

| Feature | Prio | Phase | Source | Note |
|---------|------|-------|--------|------|
| MariaDB/MySQL: create/delete DB and user | P0 | A | CP | Typed names, no string SQL |
| Import/export (gzip) | P0 | A | CP | |
| Web DB tool with SSO (Adminer or phpMyAdmin) | P1 | B | CP | Only short-lived tokens, never master credentials in the browser |
| Remote access through IP allowlist | P1 | B | CP | |
| Multiple DB servers | P2 | 2+ | CP | |
| PostgreSQL | P1 | 1 | NEW | |
| Object cache per site (Valkey, Unix socket, ADR-0045) | P1 | B | CP | Needed for the web2 migration |

## 3.5 Access, files, jobs

| Feature | Prio | Phase | Source | Note |
|---------|------|-------|--------|------|
| SSH keys and SFTP per site user (chroot optional) | P0 | A | CP | |
| Classic FTP | P2 | 2+ | CP | Off by default, SFTP preferred |
| File manager in the browser | P1 | B | CP | Operations through the agent with path confinement |
| Cron jobs per site (validator instead of deny lists) | P0 | A | CP | systemd timers as an option. Pause jobs individually and run them immediately |
| View/tail logs (access, error, PHP, app) | P0 | A | CP | Logrotate per site |
| Web terminal | P2 | 2+ | NEW | Only with strong auth and audit |

## 3.6 Security and access to the panel

| Feature | Prio | Phase | Source | Note |
|---------|------|-------|--------|------|
| Users + roles (admin, site manager, user per site) | P0 | A | CP | RBAC enforced in the API, not only in the UI |
| TOTP 2FA | P0 | A | CP | |
| Passkeys (WebAuthn) | P0 | A | NEW | Alongside TOTP from the start, Kim 2026-11-18. Phishing-resistant. Recovery through recovery codes, several passkeys per user |
| OIDC SSO | P2 | 1 | NEW | |
| API tokens with scopes, expiry date | P0 | A | NEW | |
| Audit log (who, what, when, diff) | P0 | A | CP | Immutable (append-only), exportable |
| IP/bot blocking, basic auth per site | P1 | B | CP | |
| fail2ban (SSH, panel, recidive) | P0 | A | NEW | On by default, ADR-0021 |
| CrowdSec as opt-in | P2 | 2+ | NEW | Reports IPs to an external service, ADR-0021 |
| Rate limiting and lockout at the panel login | P0 | A | NEW | |
| Malware scan (ClamAV/Maldet) | P2 | 2+ | NEW | |

## 3.7 Backups and recovery

| Feature | Prio | Phase | Source | Note |
|---------|------|-------|--------|------|
| Backup per site (files + DB) with restic | P0 | B | CP | Consistent DB dumps, encrypted, deduplicated |
| Targets: S3-compatible, SFTP, Hetzner Storage Box, Rclone backends | P0 | B | CP | |
| Schedules, retention, notification on failure | P0 | B | NEW | |
| Restore in the UI (complete, single file, single DB) | P0 | B | NEW | |
| Restore test (automatic sample) | P1 | 1 | NEW | |
| Restore on a new server (migration) | P1 | 1 | NEW | A fresh Hostashi server reads an existing backup target, shows the sites and restores them with their state. No multi-server |
| Server snapshots through cloud API (Hetzner and others) | P2 | 2+ | CP | |

## 3.8 Monitoring and notification

| Feature | Prio | Phase | Source | Note |
|---------|------|-------|--------|------|
| Service health and disk warning | P1 | B | NEW | |
| Notifications: email (SMTP), webhook, Telegram/Slack | P1 | B | NEW | |
| Prometheus endpoint | P1 | 1 | NEW | Authenticated, off by default |
| Uptime check per site | P2 | 2+ | NEW | |
| Traffic per site (MB/GB per day/week/month) | P2 | 2+ | NEW | From the access logs, no visitor analytics |

## 3.9 Caching and performance

| Feature | Prio | Phase | Source | Note |
|---------|------|-------|--------|------|
| Nginx FastCGI cache with purge | P1 | B | NEW | Simpler than Varnish, covers 80 %. Design: ADR-0046 (proposed) |
| Varnish integration | P1 | 1 | CP | Together with the CloudPanel importer (ADR-0017, Kim 2026-11-18). Own VCL and purge; the WP plugin `clp-wp-varnish-cache` is GPLv3, only usable with a compatible license. ADR-0046 (proposed) would replace it in v1 with the FastCGI cache |
| Brotli/gzip, cache header presets | P1 | B | CP | |

## 3.10 Interfaces and automation

| Feature | Prio | Phase | Source | Note |
|---------|------|-------|--------|------|
| REST API (OpenAPI 3.1) | P0 | A | NEW | The UI uses the same API, no feature only in the UI, details in [ADR-0018](adr/0018-public-rest-api.md) |
| CLI (`hostashi`) with parity to `clpctl` | P0 | A | CP | |
| Export/apply state as YAML | P1 | 1 | NEW | Basis for GitOps |
| CloudPanel importer | P1 | 1 | NEW | See below and ADR-0017 (migration, spike S6) |
| Presets per site type (WordPress, TYPO3, PHP, static …) | P1 | 1 | NEW | Typed hardening and cache building blocks instead of vhost text, ADR-0017 |
| Terraform/OpenTofu provider | P2 | 2+ | NEW | |
| Plugin system | P2 | 2+ | NEW | Only after a stable API |
| Multilingual UI (start: EN, DE) | P1 | 1 | CP | Weblate or similar |

## 3.11 CloudPanel importer (idea)

Two paths, both need checking, because CloudPanel has no documented export path and its core is closed:

1. **Adopt through the file system:** Scan existing sites, site users, Nginx vhosts, PHP-FPM pools, cron files and certificates and convert them into our state. Works independently of CloudPanel internals.
2. **Read state from the CloudPanel DB:** Faster, but coupled to unknown internals. We would interpret a local SQLite file whose schema is not documented anywhere. This is **not verified**, I found no hint of it in the documentation.

Recommendation: path 1 first. Spike in Phase 0 with a real CloudPanel test instance.
