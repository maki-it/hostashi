# 4. Architecture (proposal)

> **Planning document.** Written in November 2026 during the planning phase, before the first release, and kept as the record of why Hostashi is built the way it is. Where it differs from the product, the [ADRs](adr/) and the [user documentation](../website/docs/docs/Home.md) are current.

This was the proposal that the spikes in Phase 0 confirmed or rejected; the results are in [`spike/`](../spike/README.md) and the ADRs.

## 4.1 Overview

```text
 Browser / CLI / API clients / (later) Terraform
          │ HTTPS
          ▼
┌──────────────────────────────────────┐
│ hostashi serve        (User: hostashi) │  no root, listens on the panel port
│  • REST API + embedded SPA            │
│  • AuthN/AuthZ, audit, job status     │
│  • SQLite (state, audit, mail outbox) │
└──────────────┬───────────────────────┘
               │ Unix socket, SO_PEERCRED, typed RPC
               ▼
┌──────────────────────────────────────┐
│ hostashi agent        (User: root)     │  small, no network listener
│  • fixed list of operations           │
│  • validation, reconciler             │
│  • writes only to allowed paths       │
└───┬────────┬────────┬────────┬───────┘
    ▼        ▼        ▼        ▼
  Nginx   PHP-FPM   MariaDB   systemd / nftables / restic / ACME / useradd …
```

One binary, three modes: `hostashi serve`, `hostashi agent`, `hostashi <cli-command>`. The CLI talks to the API (token) or, as root on the server, directly to the agent.

The panel's HTTPS is nginx's (ADR-0023). nginx serves the host name of the public URL on the panel port (default 8443, TCP and UDP for HTTP/3) with a certificate that is obtained like a site certificate (name `_panel`), and proxies to the Unix socket `/run/hostashi-panel.sock`. systemd creates that socket (`hostashi-panel.socket`, `hostashi:www-data`, 0660), so only nginx's workers and the panel can connect, and the panel trusts `X-Real-IP` on this socket only. The panel itself listens on `127.0.0.1:8080` for the SSH tunnel and the health check, and never trusts a forwarded header there.

## 4.2 Privilege separation (core decision)

Panels of this kind are the number one attack target: whoever takes over the panel has root. Therefore:

- The web process runs **without root** and without `sudo`. It cannot do anything except "ask" the agent.
- The agent has **no network listener**, only a Unix socket (`/run/hostashi/agent.sock`, mode 0660, group `hostashi`). Peer credentials are checked.
- The RPC interface knows **concrete operations with typed parameters**, for example `SiteCreate{domain, type, phpVersion}`. There is **no** `Exec(cmd string)`.
- Processes are started exclusively with **argv arrays**, never through `sh -c`. Configurations are produced from Go templates over typed structs.
- Input is **validated positively** (domain by parser, user names by a fixed rule). No blacklisting of words.
- The agent only writes below allowlisted paths. File operations on behalf of a site user use `openat2`/`RESOLVE_BENEATH` (or `fchdir` into the site root plus privilege drop), so symlink tricks cannot lead outside.
- systemd hardening for both services (`NoNewPrivileges`, `ProtectSystem`, `CapabilityBoundingSet` limited to what is needed).

## 4.3 Declarative state and reconciler

1. The DB holds the **desired state** (sites, domains, DBs, cron, firewall, backup policies …).
2. The **reconciler** in the agent computes the target configuration from it (Nginx vhosts, FPM pools, systemd units, cron, nftables).
3. It writes it **atomically** into a new "generation" (`/var/lib/hostashi-agent/web/generations/<n>/`, root only), validates it (`nginx -t`, `php-fpm -t`, `nft -c`) and only then switches over and reloads.
4. If validation or the health check fails, the previous generation is restored and the error is reported in the job.
   The health check must **prove that the new generation is really live** (generation number through a control endpoint, several hits in a row) and only then check the sites. `nginx -t` does not detect a port that is already in use, for example, and a reload that fails silently keeps the old configuration (spikes S2 and S3).
5. **Drift detection:** Deviations in generated files that were changed by hand are shown in the UI (accept or overwrite).
6. **Diff preview** before applying. Idempotent, which makes it easy to start over after a crash.

**Custom adjustments:** Each site has include files for custom Nginx snippets (validated along with the rest). Whoever wants to replace the whole vhost marks the site as "unmanaged". The UI marks this clearly.

## 4.4 Isolation per site

| Layer | Measure |
|-------|---------|
| User | Own Linux user and group, home `/home/<user>`, web root below it |
| PHP | Own FPM master per site (`hostashi-php-<version>@<site>.service`, `internal/php`), pool as the site user, **Unix socket** `/run/hostashi-php/<site>/php-fpm.sock` with permissions `site-user:www-data 0660`, `open_basedir` on the home and the unit's own `/tmp` (`PrivateTmp`), pool-specific `php_admin_value`. Open: `disable_functions` defaults |
| TLS | The agent generates the key for each site (`internal/certs`), it never leaves root. The panel obtains the certificate via ACME HTTP-01 (`internal/acme`, Let's Encrypt) through a CSR only. HTTP redirects to HTTPS with a 301 as soon as the certificate covers all domains. HTTP/3 (QUIC, UDP 443) is on when nginx supports it. HSTS can be switched per site (one year, without `includeSubDomains` and `preload`) |
| Node/Python | systemd service as the site user with `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, `ReadWritePaths` on the site home |
| Resources | cgroup v2 through systemd: `MemoryMax`, `CPUQuota`, `TasksMax`, `IOWeight` |
| Network | Optional: outbound rules per site UID through nftables (`meta skuid`) |
| File system | Quota per site where possible (ext4/XFS project quota) |

Runtimes live **system-wide and read-only** under `/opt/hostashi/runtimes/<name>/<version>` and are assigned by symlink/unit. This is more reproducible than nvm per user.

## 4.5 Web server abstraction

An interface `WebServerDriver` (render, validate, reload). First implementation: **Nginx** from the distribution with Brotli modules (ADR-0020), implemented in `internal/web`. Caddy is to be evaluated as an alternative (JSON API, automatic HTTPS), see ADR-0005. The abstraction should not force the lowest common denominator. Its purpose is to make the driver replaceable later.

## 4.6 Data model (excerpt)

```text
Server ─┬─ User ── Role / SiteGrant
        ├─ Site ─┬─ Domain (primary, alias, redirect)
        │        ├─ Runtime {type, version, port|socket, limits}
        │        ├─ Certificate
        │        ├─ SiteUser (Linux) ── SshKey
        │        ├─ Database ── DbUser
        │        ├─ CronJob
        │        └─ BackupPolicy ── Snapshot
        ├─ FirewallRule
        ├─ Integration {provider, credentialRef}   (DNS, Storage, Cloud)
        ├─ ApiToken {scopes, expires}
        ├─ Job {status, log}
        └─ AuditEvent {actor, action, target, diff}
```

Secrets (DB passwords, API keys) are stored **encrypted** in the DB (key in `/etc/hostashi/`, mode 0400, agent/serve user only), are never written to logs and are shown only once.

## 4.7 Storage and operation

- **SQLite (WAL)** for the panel: no additional service, simple backups (`VACUUM INTO`). See ADR-0006.
- **Metrics:** ring buffer in the panel (for example 30 days, tiered resolution), Prometheus/Influx not required. An optional Prometheus endpoint for an existing monitoring stack is described in [ADR-0022](adr/0022-metrics-and-tracing.md).
- **Jobs:** there is no shared job queue. The panel has no root and the agent has no network, so the panel could only track long runs, not run them. Each long run is a job of the agent: restores, CloudPanel imports, jobs on external database servers, PHP installations and the file manager run in their own `hostashi-*@<id>` systemd unit and keep a JSON record in the agent's state directory; import and export of local databases run in the agent and keep their records in `database-jobs.json`. A job survives a restart of the panel, and a restart of the agent either leaves the unit running or reports the job as failed (`interrupted by an agent restart`). The panel polls a job's state. Outgoing email is the one queue in SQLite (`mail_outbox`, ADR-0044). Reviewed on 2026-11-21: a shared queue would only add a second copy of the agent's state, so it is not planned for v1.
- **Panel logs:** structured (JSON) in journald.

## 4.8 Packaging, installation and updates

- A **signed APT repository** with `.deb` packages for amd64/arm64 is the main path. In addition, a convenience script that only sets up the repository and runs `apt install hostashi`. It does not require blindly running a large script.
- The installer is **idempotent** and has a preflight (OS, RAM, ports, existing web servers). It does not overwrite anything it does not own, it offers adopt instead.
- Releases: signed (cosign), SBOM (CycloneDX/SPDX), reproducible builds as a goal.
- Updates through APT or the UI. Before that, a snapshot of the panel DB, after the update a health check, rollback on failure.
- The support matrix is in the documentation and in `hostashi doctor`. There is one CI run per OS release.

## 4.9 Technology choice (proposal)

| Area | Proposal | Alternative |
|------|----------|-------------|
| Backend/agent/CLI | **Go**: statically linked binary, easy cross-compile for arm64, good for systems programming | Rust (safer, slower development), PHP/Symfony (probably like CloudPanel, larger runtime footprint) |
| Frontend | **SvelteKit or React + Vite** as an SPA, embedded in the binary, TypeScript | HTMX + templ (less JS, weaker for terminal/live graphs) |
| API | REST + OpenAPI 3.1, streaming through SSE | gRPC (only internally between serve and agent) |
| DB | SQLite | PostgreSQL (only for multi-server) |
| ACME | lego as a library | certbot (Python dependency) |
| Backups | restic | rclone sync (no dedup/encryption), borg |
| Tests | Go tests, Testcontainers/Incus VMs, Playwright | |

Rationale and consequences: ADR-0003.
