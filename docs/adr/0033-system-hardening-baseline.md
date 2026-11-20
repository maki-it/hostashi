# ADR-0033 System hardening baseline before v1

**Status:** Proposed. **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0007 (isolation model), ADR-0015 (per-site resource limits), ADR-0019 (PHP), ADR-0021 (fail2ban), ADR-0023 (panel HTTPS), ADR-0024 (MariaDB), ADR-0028 (cron), ADR-0032 (SSH and SFTP), [`04-architecture.md`](../04-architecture.md), [`05-security.md`](../05-security.md), [`03-features.md`](../03-features.md) row "Firewall (nftables/UFW) with defaults" (P0).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

Kim wants the server hardened before v1, after the features and before the UI redesign (2026-11-20). This ADR starts with an audit of what is already in place and what is missing, then proposes one baseline and the order in which to build it.

### Already in place

| Area | State | Evidence |
|------|-------|----------|
| Privilege split | Panel runs as `hostashi` without root, the agent runs only typed operations, `os/exec` only in packages with an ADR (depguard) | [V: `packaging/systemd/hostashi.service`, `.golangci.yml`] |
| Panel unit | `CapabilityBoundingSet=` empty, `ProtectSystem=strict`, `PrivateUsers=yes`, `SystemCallFilter=@system-service ~@privileged @resources`; the container test fails above an exposure score of 2.0 | [V: `hostashi.service`, `packaging/test/container-test.sh` step "systemd exposure"] |
| Backup, restore, import units | Read-only file system, capabilities limited to what each job needs | [V: `hostashi-backup.service`, `hostashi-restore@.service`, `hostashi-import@.service`] |
| Cron units per site | `ProtectSystem=strict`, `ProtectHome=tmpfs` with only the own home bound, `RestrictSUIDSGID`, `RestrictNamespaces` | [V: `internal/cron/unit.go`] |
| PHP pools | One master per site, workers as the site user, `open_basedir` = home + private `/tmp`, `allow_url_include` off, `security.limit_extensions = .php`, `clear_env` | [V: `internal/php/php.go`] |
| File system | Pool directories `0711`, homes `0750` site:www-data, `/proc` with `hidepid=invisible` | [V: `install.sh` step `harden`, ADR-0032] |
| SSH for site users | Keys only from a root-owned file, no forwarding, SFTP-only or shell per key | [V: `packaging/ssh/10-hostashi.conf`] |
| Brute force | fail2ban with `sshd`, `hostashi-panel`, `recidive`, bans through nftables | [V: ADR-0021, `packaging/fail2ban/`] |
| Panel HTTP | CSP without `unsafe-inline`, `frame-ancestors 'none'`, `X-Frame-Options DENY`, `Referrer-Policy no-referrer`, cookies `Secure` and `SameSite=Strict` | [V: `internal/serve/auth_api.go`, `internal/ui/ui.go`] |
| nginx | `server_tokens off`, TLS 1.2 and 1.3 only with AEAD ciphers, hidden files denied except `/.well-known`, HSTS per site | [V: `internal/web/nginx.go`] |
| Secrets | `secret.key` 0400 hostashi, `hostashi.env` 0640 root:hostashi, agent state 0700 root, install log 0600 | [V: `install.sh` steps `dirs`, `key`, `config`] |
| MariaDB | Package defaults: `bind-address = 127.0.0.1`, root only through `unix_socket`; every site user has rights on one database only, from localhost | [V: Debian 13 container, `50-server.cnf`; ADR-0024] |
| Supply chain in CI | govulncheck, gosec in golangci-lint, Renovate, actions pinned by hash, license and REUSE checks | [V: `.github/workflows/go.yml`, `renovate.json`] |

### Missing or weak

| # | Gap | Evidence |
|---|-----|----------|
| G1 | **No firewall.** Every listening port is reachable from the internet. The installer says so itself ("Not yet part of this installer: … a firewall") | [V: `install.sh` header] |
| G2 | **No automatic security updates.** `unattended-upgrades` is not installed in the Debian 13 image | [V: Debian 13 container, `apt-cache policy`]. Whether Hetzner's or other providers' images ship it: [U] |
| G3 | **PHP updates do not reach the sites.** The distribution's package restarts its own `php8.4-fpm.service`, which Hostashi keeps off; the per-site masters `hostashi-php-<v>@<site>` keep running the old binary until something restarts them | [A: from the unit layout in `install.sh` step `php`; to be checked in the container] |
| G4 | **No kernel and network sysctl baseline.** Debian 13 ships almost none in `/usr/lib/sysctl.d` (core pattern, pid max); systemd adds a few defaults | [V: Debian 13 container without systemd; systemd's `50-default.conf`: U] |
| G5 | **PHP has no `disable_functions`.** Site code can start programs (`exec`, `proc_open`, …). Distribution default is empty | [V: `php.ini` of php8.4-fpm, `internal/php/php.go`] |
| G6 | **PHP master and agent units are less sandboxed than the rest.** The PHP master has `ProtectSystem=full` instead of `strict`, no `ProtectHome`, no `CapabilityBoundingSet`. The agent has no `CapabilityBoundingSet` and no `SystemCallFilter`. Only the panel unit has an exposure limit in the test | [V: unit files, `container-test.sh`] |
| G7 | **Core dumps are not switched off.** The panel holds the secret key in memory; `kernel.core_pattern=core` writes dumps next to the process | [V: Debian 13 `10-coredump-debian.conf`; no `LimitCORE` in any unit] |
| G8 | **The admin's own SSH stays as the image left it.** Debian's default allows password logins (`#PasswordAuthentication yes`) and root with keys. Hostashi deliberately touches only the `Match Group hostashi-sites` block | [V: Debian 13 `sshd_config`, ADR-0032] |
| G9 | **MariaDB `local_infile`** is on by default in MariaDB 11.8 [U]. Hostashi does not need it [V: imports run `mariadb --sandbox` on dump files, ADR-0025] | |
| G10 | **No two-site isolation test.** `05-security.md` 5.5 asks for a release-blocking test where one site tries to reach another site's files, sockets, processes and databases. Today the container test checks one site against `/etc/hostname`, and SSH against the pool listing and `/proc` | [V: `container-test.sh`] |

Not gaps for v1, named so that they are not forgotten: outbound firewall rules per site UID (`04-architecture.md`), AppArmor profiles, auditd, mount options for `/tmp` and `/dev/shm` (every Hostashi unit has `PrivateTmp`), security headers for sites (the application's job, a later preset), panel-managed firewall rules with a rollback timer (see open question 5).

## Options

The gaps are independent; the options here are about the overall approach.

| Option | Pro | Contra |
|--------|-----|--------|
| **A. One baseline, built into the installer and the agent, checked by `hostashi doctor`** | Every server gets the same state; doctor shows drift; container tests cover it on both distributions | Several small PRs; the installer gets longer |
| B. A hardening guide for the admin, nothing automatic | No risk of breaking an admin's setup | Most servers stay as the image left them [A]; contradicts "security before features" |
| C. An external hardening tool (for example a CIS role, Lynis as a checker) | Wide coverage | Ansible or similar as a new dependency; generic rules that collide with Hostashi's own units and nftables table [A]; Lynis only reports |

## Recommendation

**Option A.** Each item below is a small PR with tests, in this order (highest risk first):

1. **H1 Firewall (G1).** nftables from the distribution, one table `inet hostashi` that Hostashi owns alone, loaded by its own unit `hostashi-firewall.service` with `nft -f`. The file first destroys and recreates only this table, never `flush ruleset`, so fail2ban's table survives (ADR-0021). Inbound policy drop; allowed: established and related, loopback, the needed ICMP and ICMPv6 types (neighbour discovery, packet too big), TCP 80, TCP and UDP 443, TCP and UDP of the panel port, and every port sshd listens on (read from `sshd -T`). Outbound stays open. Lockout protection in the installer: the local port of every open SSH session (`ss`) and of the installer's own session (`SSH_CONNECTION`) is always added, and the step refuses when it finds no SSH port at all. `--no-firewall` skips the step for servers behind a provider firewall; `--firewall-allow PORT/PROTO` adds ports. `nft` runs only in the installer (shell), so no new `os/exec` exception in Go [V: depguard covers Go only]. Doctor checks that the table is loaded.
2. **H2 Security updates (G2, G3).** `unattended-upgrades` from the distribution, security origin only, automatic reboot **off**. A dpkg hook (`DPkg::Post-Invoke`) asks the agent to restart the per-site PHP masters whose binary changed, or doctor warns and the panel offers the restart [A: the hook is the simpler way, to be checked]. Doctor warns when `/run/reboot-required` exists.
3. **H3 Sysctl baseline (G4, G7).** `/etc/sysctl.d/90-hostashi.conf`: `kernel.kptr_restrict=2`, `kernel.dmesg_restrict=1`, `kernel.yama.ptrace_scope=1`, `kernel.unprivileged_bpf_disabled=1`, `net.core.bpf_jit_harden=2`, `fs.protected_symlinks=1`, `fs.protected_hardlinks=1`, `fs.protected_fifos=2`, `fs.protected_regular=2`, `fs.suid_dumpable=0`, no ICMP redirects accepted or sent, no source routing, `tcp_syncookies=1`, reverse-path filter loose (`2`). `LimitCORE=0` on the panel, agent, backup, restore and import units. Containers cannot set most of these, so the test checks the file and doctor checks the live values [V: container tests share the host kernel, `container-test.sh` header].
4. **H4 PHP defaults (G5).** New pools get `disable_functions = exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec` and `session.use_strict_mode=1`, `session.cookie_httponly=1`. A per-site switch "allow programs" (typed, audit-logged, in the presets of ADR-0017) clears the list for sites that need it. Imported sites keep the switch on until checked [A: many backup and image plugins call `exec`]. `allow_url_fopen` stays on [A: WordPress and plugins use URL streams].
5. **H5 Unit sandboxing (G6).** PHP master: `ProtectSystem=strict`, `ProtectHome=tmpfs` with the site's home bound back through the agent's drop-in (as cron does), `CapabilityBoundingSet=CAP_SETUID CAP_SETGID CAP_CHOWN CAP_KILL` [A: what the master needs to open the socket and manage workers; the test shows the rest]. Agent: `CapabilityBoundingSet` and `SystemCallFilter` only if the container tests stay green on both distributions; every exception commented as today. The container test records the exposure score of every Hostashi unit and fails if one rises.
6. **H6 MariaDB (G9).** `/etc/mysql/mariadb.conf.d/90-hostashi.cnf` with `local_infile=0`. The package's `bind-address` stays.
7. **H7 Admin SSH (G8).** Hostashi does not change the admin's or root's SSH settings, because a wrong change locks the only admin out [A]. Doctor warns when password logins or root password logins are allowed and names the setting to change.
8. **H8 Isolation test (G10).** The container test creates two sites with PHP, SSH keys and a database each, and checks from site A: no read of B's home and files, no connect to B's PHP socket, the panel socket or the agent socket, no sight of B's processes, no login to B's database, no write to B's logs. This is the release-blocking test from `05-security.md` 5.5.

## Consequences

- The installer gets four new steps (`firewall`, `updates`, `sysctl`, `mariadb-conf`), each with check and apply as in spike S5, and the matching flags. Running it twice changes nothing.
- Doctor gets new checks: firewall table loaded, ports allowed, unattended-upgrades on, reboot required, sysctl values, PHP masters on the current binary, admin SSH password logins.
- Existing servers: there are none in production yet [V: memory, Hostashi only on fresh servers], so no migration path is needed beyond re-running the installer.
- `03-features.md`: the firewall row is covered for v1 by a static default; managing extra rules in the panel is a later step.
- `05-security.md` 5.5: the isolation test becomes part of CI on both distributions.
- No new `os/exec` exception: `nft`, `sysctl` and `apt-get` run in the installer, and the agent's PHP restart goes through systemd as today.

## Risks to name openly

- **Lockout through the firewall.** A wrong port list locks the admin out of SSH. Mitigations: ports read from `sshd -T` plus the ports of open SSH sessions, refusal when no SSH port is found, `--no-firewall`, and the provider's console as a fallback [A]. A provider firewall in front (for example Hetzner Cloud Firewall) still applies on top. If the admin later moves sshd to another port, doctor warns that the firewall does not allow it, and re-running the installer picks up the new port [A].
- **Automatic updates can break a site** (a PHP minor update, an nginx module mismatch). Security-only updates and no automatic reboot keep that small, not zero [A].
- **`disable_functions` breaks some plugins** and is not a security boundary on its own; PHP has had bypasses [U]. It reduces the easy paths; the boundary stays the site user, the slice and the sandbox.
- **Sysctl values can collide with a provider's image or a container host** [A]. Doctor reports, the installer does not fight other files in `/etc/sysctl.d`.
- **This is not a pentest.** The baseline closes known gaps from the audit; the external review from `05-security.md` 5.9 is still open and has no budget (Kim).

## Open questions

1. Firewall on by default, with the ports above and `--no-firewall` as the way out? **Decided (Kim, 2026-11-20): yes.** Kim usually runs a provider or hypervisor firewall in front (Hetzner Cloud, Proxmox); both together are fine, `--no-firewall` is for servers that rely on the outer one alone.
2. `unattended-upgrades` on by default, security only, no automatic reboot? **Decided (Kim, 2026-11-20): yes.**
3. PHP `disable_functions` with the list above on by default for new sites, with a per-site switch? **Decided (Kim, 2026-11-20): yes,** imported sites start with the switch on.
4. Doctor only warns about the admin's SSH password logins, the installer never changes them? **Decided (Kim, 2026-11-20): yes.**
5. Panel-managed firewall rules (extra ports, IP allowlists for SSH and the panel) with a rollback timer: in v1 or later? Recommendation was later. **Decided (Kim, 2026-11-20): in v1;** the design is [ADR-0034](0034-firewall-rules-in-panel.md).
