# ADR-0008 Backups with restic

**Status:** Proposed. **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0003 (stack), ADR-0004 (web/agent privilege split), ADR-0010 (v1 scope: Hetzner Storage Box as the only integration at the start), ADR-0016 (storage pools), ADR-0018 (API, scope `backups:restore`), ADR-0022 (metrics), ADR-0024 (MariaDB), [`03-features.md`](../03-features.md) 3.7, [`05-security.md`](../05-security.md) 5.8.

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Backups per site (files and databases) with restic are P0, targets include the Hetzner Storage Box [V: `03-features.md` 3.7]. The Storage Box is the only integration in scope at the start [V: ADR-0010]. The short ADR-0008 in `07-decisions.md` already chose restic, consistent dumps and a restore test as a feature [V].
- Backups are always encrypted on the client side, keys are kept apart from the target, and an export guide exists [V: `05-security.md` 5.8].
- restic versions on 2026-11-19: upstream v0.19.1 (2026-07-05) [V: proxy.golang.org], Debian 13 `restic` 0.18.0 [V: `apt-cache policy` in `debian:trixie`], Ubuntu 26.04 0.18.1 [V: same in `ubuntu:26.04`].
- restic 0.18.0 has `backup --stdin-from-command`: restic starts the given command itself and stores its stdout as a file, and fails the snapshot if the command fails [V: `restic backup --help` in `debian:trixie`; failure behaviour U, to be tested]. A database dump therefore needs no second process started by Hostashi and no pipe between two programs.
- restic's SFTP backend starts `ssh` itself and accepts `-o sftp.args` [V: `restic options`]. The Storage Box offers SFTP on port 23 with SSH keys, and sub-accounts limited to one directory [U].
- restic is a CLI, not a stable Go library. ADR-0003 already decided to call it with an argument list and parse its JSON output [V: ADR-0003]. The depguard rule names a "restic wrapper" as the expected next `os/exec` exception [V: `.golangci.yml`].
- The panel database is SQLite in WAL mode. A consistent copy is `VACUUM INTO` [V: `04-architecture.md`].

## Options

### Who runs restic

| Option | Pro | Contra |
|--------|-----|--------|
| **A. The agent starts a fixed systemd unit `hostashi-backup.service`, which runs `hostashi backup run` as root, sandboxed; a systemd timer schedules it** | A run of hours does not block the agent's socket. systemd gives logs, `Nice`/`IOSchedulingClass=idle`, a read-only file system view and "only one run at a time" for free [U] | One more unit and a status file between the run and the agent |
| B. The agent runs restic inside an RPC call | Simple | An RPC that takes hours, timeouts on both sides, and the agent's sandbox would have to allow everything restic needs |
| C. The web process runs restic | No root | It cannot read the site homes or dump the databases without root, against ADR-0004 [A] |

### Where restic comes from

| Option | Pro | Contra |
|--------|-----|--------|
| **A. The distribution's package** (0.18.0 / 0.18.1) | Security updates from the distribution, signed by its archive key, same rule as nginx and PHP (ADR-0019, ADR-0020) | One minor version behind upstream, and the two distributions differ |
| B. The upstream release binary, pinned with a checksum in Hostashi's package | Current version (0.19.1), the same on both distributions | We own the updates and a second download path in the installer |

### Repository layout

| Option | Pro | Contra |
|--------|-----|--------|
| **A. One repository per server, snapshots tagged by site** | Deduplication across sites (WordPress cores are nearly identical), one key, one target, one `check` | A key opens all sites' backups |
| B. One repository per site | Separate keys | N repositories, N passwords, no deduplication across sites |

The single administrator holds every key anyway (v1 has no resellers), so B buys little [A].

## Recommendation

Options **A, A, A** (restic source decided by Kim, 2026-11-19: the distribution's package).

1. **What is backed up, per run** (decided by Kim, 2026-11-19):
   - each site's home directory (with `--one-file-system` and `--exclude-caches`), tagged `site:<name>`, `kind:files`;
   - each database of the site, dumped by `mariadb-dump --single-transaction --routines --events --triggers <db>` through `--stdin-from-command`, stored as `/<db>.sql`, tagged `site:<name>`, `kind:db`;
   - the panel: a `VACUUM INTO` copy of the panel database, `/etc/hostashi` and the agent's registries under `/var/lib/hostashi-agent`, tagged `kind:panel`. The restic password and the SSH key are not part of it (see 4).
2. **Target in v1:** SFTP, with a form made for the Storage Box (user, host, port 23, directory). The agent generates an ed25519 key for it and the panel shows the public key. The host key is fetched once, shown with its fingerprint, and pinned after the administrator confirms it; restic then runs `ssh` with `StrictHostKeyChecking=yes` and that `known_hosts` file. S3 and rclone are later, as further typed target kinds, never as free-form restic URLs.
3. **Schedule and retention** (defaults decided by Kim, 2026-11-19, adjustable in the panel and the API): daily at a random minute between 01:00 and 05:00 local time (`RandomizedDelaySec`), `forget --keep-daily 7 --keep-weekly 4 --keep-monthly 6` per site and kind, `prune` weekly, `check` weekly, and `check --read-data-subset=1/12` monthly as the restore test, so every pack file is read once a year. The panel and `/api/v1` can change the time window, the keep counts per kind (daily 1 to 90, weekly 0 to 52, monthly 0 to 120) and the share read by the monthly check, as typed fields with bounds, never as restic arguments. The agent turns them into the timer's `OnCalendar` and fixed `forget` flags. Fewer than one daily snapshot is refused.
4. **Keys:** the agent creates the repository with a random 32-byte password in `/etc/hostashi-agent/backup/` (root, 0700/0600), next to the SSH key. Without that password the backups are lost if the server dies, so it is shown to the administrator once when the target is set up and can be shown again with `hostashi backup key show` on the server (root only), never over the API.
5. **Process boundary:** a new package `internal/resticctl` is the fourth `os/exec` exception. It runs only the restic binary by absolute path, with a cleared environment (`RESTIC_REPOSITORY_FILE`, `RESTIC_PASSWORD_FILE`, `RESTIC_CACHE_DIR`), fixed sub-commands, arguments built from validated site names, database names and snapshot IDs, `--json` output parsed into types, and stderr only in the log. The dump command for `--stdin-from-command` is built in that package from a validated database name, never passed in.
6. **Agent operations:** `backup.target.set`, `backup.target.get`, `backup.hostkey.fetch`, `backup.run` (starts the unit, returns at once), `backup.status`, `backup.snapshots` (per site), `backup.restore`. Each request validates itself like the existing operations.
7. **Restore in v1:** files are restored into a new directory `<home>/restore-<timestamp>` owned by the site user, never over the live site; a database is restored into a new, empty database of the same site, which the administrator can then switch to. Restoring in place and restoring onto a fresh server (feature "restore on a new server", P1) come later.
8. **Status and alerts:** the run writes a result file (start, end, per item ok/error, snapshot IDs, bytes added). The panel shows it per site, and a failed or missing run (older than 26 hours) shows a warning. Metrics follow ADR-0022 in the metrics work. E-mail and webhooks belong to notifications (`03-features.md` 3.8).

## Consequences

- The installer installs `restic` (and `openssh-client`) from the distribution.
- New panel tables for the target settings and the schedule, `/api/v1/backups` endpoints and the scopes `backups:read`, `backups:write` and the high-risk `backups:restore` (already named in ADR-0018).
- Disk: restic's cache lives under `/var/cache/hostashi-backup`, outside site quotas (ADR-0015 already lists it) [V: ADR-0015].
- A CI job runs the restic tests against a local repository and an SFTP server in a container, never against a real Storage Box.
- The first run against Kim's Storage Box needs Kim's explicit go-ahead.
- If a site lies in another storage pool (ADR-0016), the backup reads its resolved path; nothing else changes [A].

## Open questions for Kim

1. ~~What is backed up?~~ Decided by Kim (2026-11-19): site files, database dumps and the panel, as in recommendation 1.
2. ~~Schedule and retention?~~ Decided by Kim (2026-11-19): the defaults of recommendation 3, adjustable in the panel.
3. ~~restic from the distribution or upstream?~~ Decided by Kim (2026-11-19): the distribution's package (option A).
