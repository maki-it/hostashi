# 6. Roadmap

**Assumption for all time estimates:** 2 full-time developers, one of them with Linux systems experience, frontend at least half a position. With one person the duration roughly doubles, with a larger team it does not shrink proportionally because of coordination. These are **estimates without a data basis**, please recalibrate after Phase 0.

## Phase 0: Foundation (3–4 weeks)

Goal: make decisions and test the riskiest assumptions with throwaway code.

**Decisions:** name and trademark check, license, stack, target audience, repository hosting (see [ADRs](07-decisions.md)).

**Spikes** (2–4 days each, the result is a short report):

| Spike | Question | Abort criterion |
|-------|----------|-----------------|
| S1 Agent RPC | Is typed RPC over a Unix socket with peer credentials enough, and does the code stay manageable? | If more than 30 % boilerplate per operation, introduce codegen |
| S2 Reconciler | Are generation switch, `nginx -t`, reload and rollback robust? | Failure injection: broken vhosts must never cause downtime |
| S3 Nginx vs. Caddy | Which driver fits better (rewrites, HTTP/3, FastCGI cache, templates)? | Decision backed by measurement |
| S4 Isolation | Per-site user, FPM socket, systemd sandbox, escape test | A test from site A cannot read site B |
| S5 Installer | Idempotent installer on fresh Ubuntu 24.04 and Debian 13 (amd64 + arm64) | Runs twice without changes |
| S6 CloudPanel adopt | Can a real CloudPanel instance be read in through the file system? | If fewer than 80 % of the sites are taken over cleanly, postpone the importer |
| S7 UI prototype | Svelte vs. React, terminal and live graphs, bundle size | |
| S8 Resource limits | Can project quota be enabled on fresh Debian 13 and Ubuntu 24.04 (idempotent, with reboot)? RAM per FPM master per site, limits for cron and services ([ADR-0015](adr/0015-per-site-resource-limits.md)) | If quota cannot be enabled safely, re-evaluate alternatives (user quota, separate volume) |
| S11 Brotli | Is there a maintained Brotli module for the nginx.org builds on Debian 13 and Ubuntu 24.04 ([ADR-0017](adr/0017-cloudpanel-import-and-presets.md))? | If not, gzip stays the default ([report](../spike/s11-brotli/REPORT.md)) |
| S12 Distribution nginx | Do Debian 13 and Ubuntu 26.04 deliver Brotli over HTTP/2 and HTTP/3 with the distribution nginx ([ADR-0020](adr/0020-nginx-package-source-and-brotli.md))? | If not, re-evaluate ADR-0020 ([report](../spike/s12-nginx-distro/REPORT.md)) |

**Further tasks:** market analysis (HestiaCP, ISPConfig, 1Panel, Coolify …), evaluate CloudPanel issues, go through the threat model with an external reviewer, set up repository, CI and release pipeline, `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`.

**Acceptance:** ADRs on "Accepted", spike reports available, CI builds the binary for amd64/arm64, test VM matrix in place.

## Phase 1: Alpha (8–10 weeks)

Goal: a PHP site with HTTPS and a database can be run in production on a fresh server.

- Installer/`.deb`, initial setup by token, login with TOTP and passkeys, roles, API tokens, audit log
- Site types static, PHP, Node.js, reverse proxy; Linux user, FPM pool, aliases and redirects
- Vhost editor with validation and rollback
- Let's Encrypt (HTTP-01), custom certificates, panel under its own domain
- MariaDB/MySQL management, import/export
- Cron, logs, SSH keys/SFTP
- Firewall with lockout protection, service management, dashboard
- CLI with basic commands, OpenAPI specification

**Acceptance:** 20 test scenarios (end-to-end) green on all matrix OSes, isolation test green, documentation for installation and the first site. Only for testing and volunteers, not for production.

## Phase 2: Beta (10–12 weeks)

Goal: feature parity for the most common CloudPanel use cases.

- Backups with restic (targets, schedule, retention, restore in the UI) and notifications
- Python sites, WordPress quick install, vhost templates (Laravel, Symfony, WP, Magento, Shopware …)
- Rate limiting, IP/bot blocking, basic auth per site
- DNS-01 with the first providers (INWX, Cloudflare, Hetzner DNS), wildcard certificates
- Web DB tool with SSO, Redis, file manager
- FastCGI cache, HTTP/3, resource limits and disk quota per site
- Panel update in the UI, `hostashi doctor`

**Acceptance:** at least 10 external test servers over 4 weeks without data loss, backup restore test successful, security review of API authorization.

## Phase 3: Release 1.0 (8–10 weeks)

Goal: trustworthy enough for production servers.

- Adopt mode and CloudPanel importer
- PostgreSQL, staging/cloning, zero-downtime deployments
- State as YAML (export/apply), OIDC SSO, Prometheus endpoint, Fail2ban/CrowdSec
- **External pentest** and fixes, automated restore tests
- Complete documentation (EN), UI in EN/DE, migration guides
- Signed releases, SBOM, support and update policy

**Acceptance:** no open critical or high findings from the pentest, upgrade tests from beta to 1.0, release checklist complete.

## Phase 4 and later (not planned, options only)

Multi-server (controller + agents), resellers and packages, mail integration (Mailcow/Stalwart), Docker Compose apps, Git deploy, Terraform provider, plugin system, LXC operation, cloud provider integrations (Hetzner Cloud and Robot, snapshots, firewalls), web terminal, malware scan.

## Test strategy

| Level | Tool | Content |
|-------|------|---------|
| Unit | Go tests, golden files | Generated configs, parsers, validators |
| Fuzzing | `go test -fuzz` | Domains, paths, cron, vhost snippets |
| Integration | Incus/QEMU VMs (Docker is no substitute for systemd) | Reconciler against real Nginx/FPM/MariaDB |
| End-to-end | Playwright | Core flows in the UI |
| Matrix | CI per OS × architecture | Ubuntu 22.04/24.04/26.04, Debian 12/13 × amd64/arm64 |
| Security | Isolation test, IDOR matrix, gosec, semgrep, govulncheck | Mandatory per release |
| Upgrade | Install old version, create data, update, check | Per release |

## Risks

| Risk | Likelihood | Impact | Countermeasure |
|------|------------|--------|----------------|
| A security vulnerability after release damages trust permanently | medium | high | Privilege separation, external pentest before 1.0, disclosure process, honest "alpha/beta" labeling |
| Maintenance effort for OS/PHP/DB versions eats up the team | high | high | Keep the matrix small, templates instead of code, automation, define support periods |
| Dependency on third-party repositories (sury.org, nginx.org, MariaDB) | medium | medium | Pin keys, mirror option, documentation of the fallback source |
| Legal problems from taking over CloudPanel content | low with discipline | high | Clean-room, review rule "copy nothing from reference repositories", ADR-0001 |
| Name or trademark conflict | medium | medium | Trademark and domain search in Phase 0 |
| Scope creep (mail, DNS, billing) | high | high | Non-goals in [vision](02-vision-scope.md), changes only through ADR |
| Too little adoption against established panels | medium | high | Clear differentiation (security, import, openness), documentation, community work |
| Single-person dependency (bus factor) | high | high | Win a second maintainer early, document decisions |
