# ADR-0016 Per-site storage location (storage pools)

**Status:** Proposed (not accepted). **Date drafted:** 2026-11-17. **Decides:** Kim. **Related:** ADR-0015 (resource limits and quota), ADR-0004 (web/agent split), ADR-0007 (isolation model), ADR-0008 (backups). **Needs spike:** S9 (proposed, see below).

## Context

- Kim wants to optionally choose where a site lives, so that large sites can sit on other mountpoints, disks or volumes [V: Kim, thread, 2026-11-17].
- Today's plan puts every site under `/home/<user>` with the web root below it [V: `04-architektur.md` 4.4]. There is no way to choose a location.
- Hostashi is installed on a freshly provisioned single server [V: Kim, thread, 2026-11-17, ADR-0009]. Extra disks are typically provider volumes (for example Hetzner Volumes) or additional partitions, mounted by the admin [A].
- ADR-0015 recommends filesystem project quota per site. Quotas are a property of one filesystem, so a second filesystem needs its own quota setup [U].
- Security rules: the web process runs without root, the agent executes only typed operations, no shell strings, no general exec [V: `05-sicherheit.md`]. A location choice is exactly the kind of input that could turn into a path traversal or an arbitrary-write primitive if it is a free-form path.
- Multi-server is a non-goal for v1 [V: ADR-0010]. This ADR is about several filesystems on one server.

## Owner input (2026-11-17)

- The pool of a site is chosen **only when the site is created** at first. Moving a site between pools comes later as a separate step [V: Kim, thread].
- A pool must be its **own mountpoint** (separate volume or partition). Plain directories on the same filesystem are not accepted as pools in v1 [V: Kim, thread].
- A site's database stays in the MariaDB default data directory and does not follow the pool. This is documented in the UI and docs [V: Kim, thread].
- A site's database stays in the MariaDB default data directory and does not follow the pool. This is documented in the UI and docs [V: Kim, thread].

## Findings

### 1. Quota is per filesystem

- Project quota (ADR-0015) counts blocks and inodes within one filesystem. A site on volume B is limited only if volume B has quota enabled [U].
- Project IDs must be unique per host in practice, so they should come from one global allocator even though each filesystem keeps its own table [A].
- Hostashi must **not** format, partition or resize disks. That is destructive and outside the scope of a hosting panel [A]. The admin provides a mounted filesystem. Hostashi checks it and, with explicit confirmation, can enable quota features on it (the first-run setup does this for the default filesystem, see ADR-0015 item 8). For a provider volume this is easy at creation time because it can be unmounted [A].

### 2. The unmounted-volume trap

If a volume is not mounted at boot (mount failed, provider detached it), a site path such as `/mnt/vol1/sites/example` still exists as an ordinary directory on the **root filesystem** after the mountpoint is recreated, or writes land under the empty mountpoint. The site would silently fill the root disk, which is exactly the problem ADR-0015 tries to prevent [A, well-known failure mode]. Mitigations to test [A]:

- Pool registration records the filesystem identity (device and mount ID from `/proc/self/mountinfo`, plus a marker file `.hostashi-pool` containing a random pool ID, created at registration).
- Every operation and every service start checks that the pool path is a mountpoint and the marker matches.
- Site units use `RequiresMountsFor=<pool path>` so systemd does not start them without the mount [U].
- Optional: mark the empty mountpoint directory immutable (`chattr +i`) while unmounted so nothing can be written there [U].

### 3. Path handling in the agent

- The web process and API never send a filesystem path. They send a **pool name**. The agent resolves the name through its own registry, which only root-level pool operations can change [A].
- Pool registration (admin-only, typed operation `RegisterStoragePool{name, path}`) validates: absolute path, no `..`, resolved without following symlinks (`openat2` with `RESOLVE_NO_SYMLINKS`), is a mountpoint of a supported filesystem (ext4 or XFS), directory owned by root with mode 0755, not inside system trees (`/`, `/etc`, `/usr`, `/var/lib/mysql`, `/proc`, `/sys`, `/dev`, `/run`, `/boot`), not nested inside or containing another pool [A].
- Mount options `nosuid,nodev` are required or at least warned about. `noexec` would break some runtimes and is not required [A].
- Site directories are `<pool path>/<site-user>/`, created by the agent with the site's UID. Names come from the same validated site-user rules that already exist for `/home` [V: ADR-0004 and path rules in `05-sicherheit.md` require validation of paths and usernames; exact rules not yet written].

### 4. Where the home directory points

- Option 1: the site user's home directory **is** the pool path (`<pool>/<user>`). No symlinks, quota and mount checks apply directly.
- Option 2: keep `/home/<user>` and symlink to the pool. Rejected: symlinks complicate confinement (`RESOLVE_BENEATH`), `ProtectHome` and backups [A].

### 5. Backups and moves

- Backups use the site's resolved path. The restic job and restore must go to the **same pool** by default and fail clearly if the pool is missing or unmounted [A].
- Moving a site between pools is a cross-filesystem copy, not a rename. Sketch [A]:
  1. Check free space and quota on the target.
  2. Put the site into maintenance (stop or block writes), copy with a typed internal copy routine that preserves ownership, permissions, ACLs, xattrs, hard links and sparse files (no shell `rsync` string; a vetted library or a fixed-argument helper is open for the spike).
  3. Verify (file counts, sizes, checksums on a sample).
  4. Switch through the reconciler: home path, FPM socket and pool config, Nginx root, systemd units, project quota ID on the new filesystem.
  5. Keep the old copy as rollback until the admin confirms, then delete.
- Downtime is proportional to site size [E: minutes for typical sites, much longer for hundreds of GB]. A two-pass copy (live first pass, short final pass in maintenance) shortens it [A]. The move must be resumable and idempotent, matching the reconciler principles [V: `04-architektur.md` 4.3].
- Database data stays where MariaDB keeps it. A site's DB size is not affected by the pool. Moving the MariaDB data directory is a separate topic [A].

### 6. Monitoring

- Show free space per pool, warn at thresholds, and refuse to create new sites on a pool that is almost full [A].
- A pool without quota support is allowed but visibly marked, and sites on it get no disk limit until quota is enabled (same behavior as ADR-0015 item 8).

## Options

### (a) Free path per site

The admin types any path. Rejected: it hands the API a path, the largest risk class in the threat model, and ties every other feature (quota, backup, mount checks) to ad-hoc validation.

### (b) Named storage pools (recommended)

The admin registers pools once (default pool `default` = the standard site directory on the first filesystem). A site references a pool by name. Everything above (validation, quota, mount checks, backups, moves) hangs off the pool object.

### (c) Bind mounts or symlinks managed by the admin outside Hostashi

Zero work for Hostashi: the admin mounts a volume at `/home/<user>` for a heavy site. Works today and stays possible, but Hostashi cannot check mounts, quota or free space, and moves are manual. Remains a supported manual workaround, not the feature.

### (d) One filesystem only, no choice

Simplest. Contradicts Kim's wish.

## Recommendation

**Option (b), named storage pools.**

1. Add a **pool** concept to the site model: every site has a `pool` field, default `default`. The data model change is cheap now and expensive later [A], so it should land in the first schema even if the UI for several pools comes later.
2. Pools are registered by an admin-only, typed agent operation with the validation in finding 3. The pool registry is stored in Hostashi state and in a root-owned file the agent reads, not trusted from the web process [A].
3. The site user's home directory is `<pool path>/<user>`, no symlinks.
4. Quota (ADR-0015) is enabled and checked **per pool**. Hostashi does not format or partition. It can enable quota features on an already-mounted pool after the same preview and confirmation rules as the first-run setup.
5. Mount safety per finding 2 is required before a second pool can be used: marker file, mountpoint check, `RequiresMountsFor`.
6. The pool is chosen at site creation only (Kim's decision). Moving a site between pools is a later, separate operation with the sketch in finding 5, and until it exists a site stays on its pool.
7. Phasing: `pool` field and the default pool in Alpha, additional pools in Beta together with disk quota, moving sites afterward [A, subject to Kim's priority].

## Consequences

- Positive: large sites can live on their own volume, the root filesystem is protected from runaway sites, and the API still never receives a path.
- Negative: more state to keep consistent (pool registry, mount checks, per-pool quota), a more complex backup and restore story, and a cross-filesystem move operation that needs careful testing.
- Follow-ups if accepted: `03-features.md` (new rows for storage pools and site moves), `04-architektur.md` 4.4 and the site model, `05-sicherheit.md` (pool registration validation, new agent operations), ADR-0015 cross-reference, `hostashi doctor` checks per pool.

## Spike S9 (proposed): storage pools

Time box: 2 to 3 days [E]. Questions:

1. Does the marker file plus mountpoint check reliably prevent writes to an unmounted pool, including after a reboot with a missing volume, and does `RequiresMountsFor` stop the site units?
2. Does project quota work on a second ext4 or XFS volume, and can the first-run logic enable it on an unmounted fresh provider volume?
3. Does a cross-filesystem copy preserve ownership, ACLs, xattrs, hard links and sparse files, and how long does a 50 GB site take with a live first pass?
4. Which copy implementation fits the "no shell strings" rule (pure Go routine versus fixed-argument helper)?
5. Does pool path validation with `openat2` hold against symlink and bind-mount tricks (fuzz and negative tests)?

## Open questions

None at this level. Remaining before Accepted: Kim sets the status, and spike S9 must not contradict the design.
