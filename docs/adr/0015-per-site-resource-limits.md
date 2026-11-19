# ADR-0015 Per-site resource limits

**Status:** Proposed (not accepted). **Date drafted:** 2026-11-17. **Decides:** Kim. **Related:** ADR-0007 (isolation model), ADR-0004 (web/agent split), ADR-0005 (web server), ADR-0003 (stack). **Spike:** S8 done, see `spike/s8-quota/REPORT.md` and the section "Spike S8 results" below. **Last updated:** 2026-11-19.

## Context

- Kim's main complaint about CloudPanel: a site can consume unlimited disk space [V: Kim, thread, 2026-11-17]. A single runaway site (log flood, upload abuse, forgotten backups) can fill the disk and take down all other sites.
- Current state: 1 server, 22 sites, Kim as the only operator [V: Kim, thread]. Hostashi is single-server in v1 [V: `02-vision-scope.md`, ADR-0010].
- The isolation model already gives every site its own Linux user and its own FPM pool, plus "cgroup limits" [V: `02-vision-scope.md`, `04-architecture.md` 4.4, ADR-0007 in `07-decisions.md`]. The architecture lists `MemoryMax`, `CPUQuota`, `TasksMax`, `IOWeight` and "filesystem quota per site where possible" [V: `04-architecture.md` 4.4], but does not say how, and the details below are not yet designed.
- Feature matrix today: resource limits (CPU, RAM, tasks, I/O) are P1 / Beta, disk quota is P2 / "2+" [V: `03-features.md`]. Given Kim's priority, the disk quota priority looks too low (see Recommendation).
- Target platforms: Debian 13 and Ubuntu 26.04 [V: ADR-0020, which dropped Ubuntu 24.04 for v1]. cgroup v2 with the `cpu io memory pids` controllers is active on Debian 13 [V: S8]; Ubuntu 26.04 is expected to match [U].
- Spike S8 measured project quota and FPM master memory (see below). cgroup limits under load were not measured [V: S8 report].

## Owner input (2026-11-17)

- Kim's current server (`web2`) uses ext4 on a single `/dev/sda1`, 38 GB, about 27 GB used, no quota mount options [V: Kim, `findmnt` and `df` output in thread].
- Hostashi will run on a **completely fresh server**, not on `web2` [V: Kim, thread]. Effect: quota can be prepared at provisioning time. A reboot during first setup costs nothing, so the maintenance-window concern for existing servers does not apply to Kim's own use. It stays relevant for later adoption of existing servers (S6) and other users.

- First-run setup should **enable quota itself**, not only detect it [V: Kim, thread, 2026-11-17].

## What "resource" means here

Five different things, each with a different mechanism. They should not be forced into one "limits" feature:

| Resource | Mechanism | Hard enforcement possible? |
|----------|-----------|----------------------------|
| Disk space (files) | Filesystem quota | Yes (writes fail with `EDQUOT`) |
| Disk space (database) | None in MariaDB | No, monitoring only |
| RAM, CPU, tasks, I/O | cgroup v2 via systemd | Yes |
| Concurrent PHP workers | PHP-FPM pool settings | Yes |
| DB connections | MariaDB per-account limits | Yes |
| Requests, bandwidth | Nginx `limit_req`, `limit_conn` | Rate yes, volume quota no |

## Findings per resource

### 1. Disk space (files): the main problem

Three mechanisms exist [U for all details below]:

- **User quota** (`usrquota`): counts every file owned by the site's UID on that filesystem. Simple, works because each site has its own UID. Weakness: files created by another UID (for example by a root-run restore, or `www-data`) are not counted, and files owned by the site user outside the web root count too (which is mostly wanted).
- **Project quota** (`prjquota`, ext4 and XFS): counts all files below a directory tree that carry the same project ID, whatever their owner. This matches "limit this site's directory" best and survives ownership mistakes. Needs a project ID per site and the inheritance flag on the site directory.
- **Other filesystems:** Btrfs qgroups, ZFS `refquota`, LVM volume per site. Heavier and distribution specific. Out of scope unless the target filesystem requires it.

Practical concerns:

- Enabling quotas is a filesystem and mount-level change. On ext4 the `project` feature may need an unmounted filesystem for `tune2fs`, and the `prjquota` mount option needs a remount or reboot [U]. On XFS the quota mount option must be present when the filesystem is mounted, which for the root filesystem means a kernel command line option [U]. This is the largest risk: Hostashi cannot assume it can enable quotas on an existing server without a reboot or maintenance window. **Needs a spike on Kim's real server layout.**
- Hard limit makes writes fail, which the application sees as "disk full" for that site only. This is the desired outcome but can corrupt half-finished writes in badly written applications [A]. Warn before the hard limit.
- Quotas can limit blocks **and** inodes. Inode limits protect against millions of tiny files (cache directories) [U].
- Reading usage of other users needs privilege (`quotactl`), so usage reporting is an **agent operation**, not something the rootless web process does [A, consistent with ADR-0004].
- **What quotas do not cover:** database files (`/var/lib/mysql`), logs written outside the site directory (Nginx access and error logs, PHP-FPM logs), local restic cache, and `/tmp`. Mitigation: per-site logs inside the site tree with logrotate, `PrivateTmp=yes` (already planned for Node/Python services, [V: `04-architecture.md` 4.4]) or a tmp directory inside the site tree, and a combined "storage used" figure in the UI (files + DB).

### 2. CPU, RAM, tasks, I/O

- systemd properties map directly to cgroup v2 controls: `MemoryMax`, `MemoryHigh`, `CPUQuota`, `CPUWeight`, `TasksMax`, `IOWeight` [V for the first, third, fourth and fifth in `04-architecture.md`; the others are U].
- **Key design constraint:** one PHP-FPM master process lives in one cgroup, and all its pools share it [U]. Per-site pool settings (`pm.max_children`, `rlimit_files`, `request_terminate_timeout`, `memory_limit`) work with a shared master, but **per-site cgroup limits do not**. To put a site's PHP into its own cgroup there must be **one FPM master per site**. This is implemented: every site runs `hostashi-php-<version>@<site>.service` [V: PR 52, `internal/php`]. Putting that unit into `Slice=hostashi-site-<name>.slice` is not done yet [V].
- Cost of one master per site: about 7 to 10 MB per idle `ondemand` master, about 160 to 230 MB for 22 sites [V: S8, one clean run, PHP 8.3].
- `pm = ondemand` keeps idle sites near zero worker memory but adds start latency on the first request after a pause [U]. Suggested default for small sites, with `dynamic` or `static` available per site for busy ones [A].
- Cron jobs, queue workers and Node/Python services must start inside the site's slice (systemd timers and services instead of classic cron, or `systemd-run --slice=…`), otherwise they escape the limits [A]. Interactive SSH/SFTP sessions land in logind's per-user slice, which can carry its own limits [U]. Open: whether to apply them there.
- Memory semantics: `MemoryMax` kills processes in the cgroup when exceeded, `MemoryHigh` throttles first [U]. Suggested pair: `MemoryHigh` at about 90 percent of `MemoryMax` [E]. Swap behavior (`MemorySwapMax`) needs a decision per host.
- CPU: a hard `CPUQuota` wastes idle capacity, `CPUWeight` shares fairly under contention. Suggested default: weights only, quota as an optional per-site cap [A].
- I/O: `IOWeight` needs a supported I/O scheduler and cgroup-aware writeback to be effective [U]. Treat as best effort, verify in the spike.

### 3. Database

- MariaDB has **no per-database or per-user size quota** [U]. A hard limit is not available.
- Per-account limits do exist: `MAX_USER_CONNECTIONS`, `MAX_QUERIES_PER_HOUR` and similar [U]. Use connection limits by default, leave query limits optional.
- Size can be measured (`information_schema.TABLES`, `data_length + index_length`), which is an approximation for InnoDB and does not include undo logs or the shared tablespace [U].
- Enforcement option for later: when a database exceeds its budget, `REVOKE` write privileges so the site becomes read-only for the DB. Surprising behavior, can break applications mid-transaction, so it must be explicit opt-in [A].

### 4. Requests and bandwidth (Nginx)

- `limit_req` and `limit_conn` per site are cheap and available [U]. They are abuse protection more than accounting. Behind a CDN or proxy they need correct real-IP handling, otherwise all visitors share one bucket [U].
- `limit_rate` throttles single connections, it is not a monthly quota.
- **Traffic volume quotas** (GB per month) would need log or counter accounting, a reset schedule and a policy for what happens at the limit. This is a hosting-provider feature, close to the billing and reseller non-goals [V: `02-vision-scope.md`]. Defer.

## Options

### (a) Do nothing special beyond the architecture sketch

Keep disk quota at P2, resource limits at P1 as listed. Pro: no new work. Contra: leaves Kim's main complaint for "later".

### (b) Disk quota first, cgroup limits second, DB monitoring only, traffic quotas deferred (recommended)

Make the disk quota the first deliverable of this feature area and ship cgroup limits alongside the per-site FPM masters. Everything is exposed as a few typed agent operations and as fields in the site model, applied by the reconciler.

### (c) Containers per site (Podman/Docker) for all limits

Container runtimes give CPU, RAM and (with the right storage driver) disk limits in one mechanism. Rejected as default: ADR-0007 already chose "no container runtime as a hard dependency", and the per-site idle RAM and operational surface grow [V: ADR-0007]. Stays an optional site type later.

### (d) Per-site filesystem (LVM volume or loop-mounted image)

Strongest disk isolation, size is fixed by the volume. Contra: resizing is awkward, many mounts, backup and adoption of existing sites get complicated, loop devices add overhead [A]. Not recommended for v1 unless the spike shows the host filesystem cannot do quotas.

## Recommendation

**Option (b).**

1. **Disk quota is the priority.** Move "disk quota per site" from P2 / 2+ to P1 and, if the spike confirms feasibility on the target filesystem, plan it for Beta together with the cgroup limits, or earlier. Without it the Hostashi isolation story has the same gap Kim reports in CloudPanel.
2. **Prefer project quota** (ext4 or XFS) over user quota, with user quota as a fallback when project quota is unavailable [A]. Block and inode limits per site. Warning thresholds (for example 80 and 95 percent) are panel-side from usage read through the agent, the kernel hard limit is the backstop.
3. **One systemd slice per site** (`hostashi-site-<name>.slice`) holding that site's FPM master, services, timers and cron jobs. Limits: `MemoryMax`, `MemoryHigh`, `CPUWeight` (optional `CPUQuota`), `TasksMax`, `IOWeight`. Default limits come from **presets** so the UI does not need ten fields. Decided (Kim, 2026-11-19): presets are data, not code. The admin creates, edits and deletes them in the panel and API and marks one as the default for new sites. Every value can be overridden per site. The installer seeds three presets [E: estimates for typical WordPress sites, not measured]:

   | Preset | Disk | Inodes | `MemoryMax` | PHP workers (`pm.max_children`) | `TasksMax` | CPU priority |
   |--------|------|--------|-------------|----------------------------------|------------|--------------|
   | small (default) | 10 GiB | 500,000 | 512 MiB | 5 | 128 | normal |
   | medium | 25 GiB | 1,000,000 | 1 GiB | 10 | 256 | high |
   | large | 50 GiB | 2,000,000 | 2 GiB | 20 | 512 | high |

   `MemoryHigh` is 90 percent of `MemoryMax`. Changing a preset updates the sites that use it without overrides.

   **CPU (Kim, 2026-11-19):** no hard limit by default. Between sites, a "CPU priority" mode low / normal / high maps to `CPUWeight` 50 / 100 / 200; a free `CPUWeight` value (1 to 10000) is possible per site. Inside a site, cron jobs and background services run with `Nice=10` (changeable per job), so the PHP workers serving visitors win over a long script of the same site. Niceness is not used between sites: each site has its own cgroup, so only the weight decides between them [U: standard cgroup v2 scheduler behaviour, not measured on the target]. A hard `CPUQuota` stays an optional per-site value, off in all seeded presets.
4. **`pm = ondemand` as default**, `dynamic` or `static` for busy sites, `pm.max_children` derived from the preset [A]. Keeping this per-site means a runaway site hits its own ceiling first.
5. **Database:** connection limit per DB user by default, size shown and warned about, **no automatic enforcement in v1**. Read-only lock is a later opt-in.
6. **Nginx:** optional per-site `limit_req` and `limit_conn` presets, off by default. No traffic quotas in v1.
7. **Defaults for new and imported sites:** every new site gets a preset with a disk limit, "unlimited" must be an explicit choice. Imported or adopted sites start with their measured usage plus 50 percent headroom, at least the default preset, to avoid surprise write failures (Kim, 2026-11-19). "Unlimited" is an explicit choice, see open question 2.
8. **First-run setup enables quota itself** (Kim's decision). Proposed guard rails [A]: the setup first detects filesystem, mount options and quota support and shows exactly what it will change (preview, like the reconciler diff). Changes that touch the filesystem or need a reboot (for example `tune2fs` feature flags, `/etc/fstab` mount options) require an explicit confirmation by the admin, either a prompt or a flag such as `--enable-quota`, never silently. It backs up `/etc/fstab` before editing and validates the new entry before the reboot. If quota cannot be enabled safely (unsupported filesystem, root on a layout the setup does not know), it stops with a clear message and leaves the server unchanged, and Hostashi runs without disk limits until quota is available, visibly marked in the UI and in `hostashi doctor`. The setup is idempotent: a second run detects that quota is already active and changes nothing. Offline steps that cannot run on a mounted root (for example setting the ext4 `project` feature) may need a reboot into a one-shot initramfs or rescue step, which is a risk to test in S8 [A].
9. **Mechanics respect the security rules:** the agent exposes typed operations such as `SetSiteDiskQuota{site, bytes, inodes}`, `SetSiteResourceLimits{site, preset|values}` and read-only `GetSiteUsage{site}`. Values are validated numeric ranges, site names resolve to known site records, no free-form unit text, no shell strings. systemd is driven through D-Bus or fixed argument vectors, and unit files are rendered by the reconciler with the diff preview from the architecture [V: `04-architecture.md` 4.3].

## Consequences

- Positive: one noisy site cannot fill the disk or starve the others. Limits reuse kernel features, so runtime overhead is expected to be small [A]. The security model stays intact (typed agent operations).
- Negative: one FPM master per site adds some idle memory and more units to manage. Quota setup depends on the host filesystem and may need a reboot or maintenance window on existing servers. Database size cannot be hard-limited.
- Roadmap and feature matrix change if accepted: disk quota and resource limits move up in `03-features.md` and `06-roadmap.md`, a spike is added.
- Follow-ups if accepted: update ADR-0007 text (per-site FPM master), `03-features.md` (priority and phase), `04-architecture.md` 4.4 (quota mechanism), `05-security.md` (new agent operations in the operation list), and the installer and first-run setup to detect, enable and report quota support (`hostashi doctor`).

## Spike S8 (proposed): resource limits on the target server

Time box: 2 to 4 days [E]. Answers, each with a measurement or a test:

1. Which filesystem layout is best for the fresh target server, and can project quota be enabled on a stock Hetzner Ubuntu 24.04 image, with or without reboot? What does the same question look like on a fresh Hetzner Debian 13 image?
2. Does a project quota correctly stop one site from filling the disk, and do the other sites keep working? What do PHP, WordPress and a Node app do when they hit `EDQUOT`?
3. How much RAM does one idle `ondemand` FPM master cost, and what is the first-request latency after idle? Total for 22 sites, compared with a single shared master.
4. Do `MemoryMax`, `MemoryHigh`, `TasksMax` and `CPUWeight` behave as expected for a PHP site and a fork bomb test, without affecting a neighbor? Does `IOWeight` do anything measurable on the target disk?
5. Do cron jobs and SSH sessions stay inside the site's limits with the chosen mechanism?
6. Can the first-run setup enable project quota on a stock image safely and idempotently, including the offline step and reboot, and what happens if it is interrupted?
7. Can the agent read quota and cgroup usage cheaply enough for a dashboard poll?

Abort criteria: if project quota cannot be enabled without unacceptable downtime and user quota is also unusable, re-evaluate option (d) before accepting this ADR.

## Spike S8 results (2026-11-17)

- Project quota on a stock ext4 root needs **one reboot**: the `project` and `quota` features can only be set on an unmounted filesystem, so the setup sets them from an initramfs hook, which must also add `quota_v2` to the initramfs, otherwise the root mount fails [V: S8 findings 1, 2].
- Mounting with `prjquota` alone may only count usage; enforcement needs `quotaon -P /` after boot [V: S8 finding 3, behaviour differed between the VM and the Hetzner server].
- Root bypasses quota (`CAP_SYS_RESOURCE`), so restores and other root work in a site directory can exceed the limit [V: S8 finding 4].
- Kim ran the prototype on a fresh Hetzner Debian 13 cloud image: one reboot, limits enforced, neighbour unaffected, second run changes nothing [V: S8 report].
- Not measured: cgroup limits under load, cron and SSH inside the limits, `IOWeight`, polling cost with many sites [V: S8 report].

## Open questions

1. ~~Which layout for the fresh server?~~ **Answered by S8:** the provider's default image (ext4 root) plus one reboot during the installer's quota step. XFS and separate data volumes stay possible later through storage pools (ADR-0016) but are not part of v1 setup.
2. ~~Should "unlimited" stay selectable in the UI at all, or only via CLI?~~ **Answered (Kim, 2026-11-19):** "unlimited" is selectable in the UI and the API, never the default. It must be an explicit choice, and sites without a limit are visibly marked.
3. ~~Should SSH/SFTP sessions count against the site's limits, or only the web workload?~~ **Answered (Kim, 2026-11-19):** yes. The disk quota covers all files of the site anyway. The site's CPU and memory limits also apply to the user's login slice (`user-<uid>.slice`, set through a systemd drop-in), so a shell job cannot slow down the server. Built together with SSH/SFTP per site, which does not exist yet.
