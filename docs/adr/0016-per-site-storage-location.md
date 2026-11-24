# ADR-0016 Per-site storage location (storage pools)

**Status:** Accepted (Kim, 2026-11-21). **Date drafted:** 2026-11-17. **Decides:** Kim. **Related:** ADR-0015 (resource limits and quota), ADR-0004 (web/agent split), ADR-0007 (isolation model), ADR-0008 (backups). **Needs spike:** S9 (proposed, see below).

## Context

- Kim wants to optionally choose where a site lives, so that large sites can sit on other mountpoints, disks or volumes [V: Kim, thread, 2026-11-17].
- Today's plan puts every site under `/home/<user>` with the web root below it [V: `04-architecture.md` 4.4]. There is no way to choose a location.
- Hostashi is installed on a freshly provisioned single server [V: Kim, thread, 2026-11-17, ADR-0009]. Extra disks are typically provider volumes (for example Hetzner Volumes) or additional partitions, mounted by the admin [A].
- ADR-0015 recommends filesystem project quota per site. Quotas are a property of one filesystem, so a second filesystem needs its own quota setup [U].
- Security rules: the web process runs without root, the agent executes only typed operations, no shell strings, no general exec [V: `05-security.md`]. A location choice is exactly the kind of input that could turn into a path traversal or an arbitrary-write primitive if it is a free-form path.
- Multi-server is a non-goal for v1 [V: ADR-0010]. This ADR is about several filesystems on one server.

## Owner input (2026-11-17)

- The pool of a site is chosen **only when the site is created** at first. Moving a site between pools comes later as a separate step [V: Kim, thread].
- A pool must be its **own mountpoint** (separate volume or partition). Plain directories on the same filesystem are not accepted as pools in v1 [V: Kim, thread].
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
- Site directories are `<pool path>/<site-user>/`, created by the agent with the site's UID. Names come from the same validated site-user rules that already exist for `/home` [V: ADR-0004 and path rules in `05-security.md` require validation of paths and usernames; exact rules not yet written].

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
- Downtime is proportional to site size [E: minutes for typical sites, much longer for hundreds of GB]. A two-pass copy (live first pass, short final pass in maintenance) shortens it [A]. The move must be resumable and idempotent, matching the reconciler principles [V: `04-architecture.md` 4.3].
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
- Follow-ups if accepted: `03-features.md` (new rows for storage pools and site moves), `04-architecture.md` 4.4 and the site model, `05-security.md` (pool registration validation, new agent operations), ADR-0015 cross-reference, `hostashi doctor` checks per pool.

## Spike S9 (proposed): storage pools

Time box: 2 to 3 days [E]. Questions:

1. Does the marker file plus mountpoint check reliably prevent writes to an unmounted pool, including after a reboot with a missing volume, and does `RequiresMountsFor` stop the site units?
2. Does project quota work on a second ext4 or XFS volume, and can the first-run logic enable it on an unmounted fresh provider volume?
3. Does a cross-filesystem copy preserve ownership, ACLs, xattrs, hard links and sparse files, and how long does a 50 GB site take with a live first pass?
4. Which copy implementation fits the "no shell strings" rule (pure Go routine versus fixed-argument helper)?
5. Does pool path validation with `openat2` hold against symlink and bind-mount tricks (fuzz and negative tests)?

## Open questions

None at this level. Remaining before Accepted: Kim sets the status, and spike S9 must not contradict the design.

## Addendum 1: adding pools and the results of spike S9

**Status:** Accepted (Kim, 2026-11-21). **Decides:** Kim. **Related:** [spike S9 report](../../spike/s9-storage-pools/REPORT.md).

### Context

- Spike S9 ran a second pool on an ext4 loop volume in a Debian 13 systemd container [V: S9 report]. Sites on it work. The agent refuses to create or delete sites on a pool whose volume is missing, and it refuses symlinked pools, wrong markers and plain directories [V].
- The gaps it found: the site units saw the other pools (`ProtectHome=` hides only `/home`), no site unit waited for its volume, the page cache could write below a missing volume, and there was no way to add a pool [V: S9 findings 1 to 5]. The first three are fixed (PR #319). This addendum is about adding pools.
- `RequiresMountsFor=` only works when the volume has a mount unit, which in practice means an `/etc/fstab` entry. Without one, a unit starts on the bare mountpoint [V: S9 finding 2].

### Decision (recommended)

1. **Root only, on the command line.** `hostashi pool add NAME PATH`, `hostashi pool list` and `hostashi pool remove NAME` call typed agent operations (`pool.add`, `pool.list`, `pool.remove`). The agent accepts add and remove only from root, never from the panel, because they carry a path and change what every site unit hides. The panel will only list pools and let the admin pick one for a new site (later PR).
2. **Hostashi never formats, partitions or mounts.** The admin mounts the volume and adds it to `/etc/fstab` first (`nofail` recommended). `pool add` checks, and refuses with the reason:
   - the name follows the pool name rule and is new; the path is absolute, clean and only `A-Za-z0-9_./-`;
   - the path is not in a system tree (`/bin`, `/boot`, `/dev`, `/etc`, `/lib*`, `/proc`, `/root`, `/run`, `/sbin`, `/sys`, `/tmp`, `/usr`, `/var`) and does not overlap another pool (so nothing below `/home`);
   - it is opened without following symlinks, owned by root, the **root of a mount**, ext4 or XFS, mounted read-write with **`nosuid,nodev`**;
   - it has an `/etc/fstab` entry;
   - it is **empty** apart from `lost+found`, so no old marker or data from another server mixes in.
3. Then the agent sets the pool root to root `0711` (like `/home`, ADR-0032), writes the marker `.hostashi-pool` with a new random ID and records the pool with `requireMount`. It writes the PHP drop-ins and cron units of all sites again so they hide the new pool (PHP masters restart once). The file manager follows at its next start.
4. `pool remove` only works for a pool without sites and never for `default`. It deletes the marker, so the volume can be unmounted or added again.
5. Quota per pool, doctor checks per pool, the panel and the import follow in their own PRs (S9 proposal steps 3 to 6).

### Consequences

- Positive: a second volume can be used without editing state files, and every check of finding 3 runs before the first site lands there.
- Negative: an admin who mounts a volume without `nosuid,nodev` or without an fstab entry is refused and has to fix the mount first. This is intended.
- Open: XFS is accepted but not tested yet [U]; a reboot with a missing volume on a real host is not tested [U].

## Addendum 2: rename pools and choose the default pool (Proposed)

### Context

- Kim wants to rename an existing pool and make a new or existing pool the default, both on the command line and in the panel, and to see the pools with their usage in the panel (Kim, 2026-11-23).
- Today the default pool is fixed by name: a site that names no pool goes to the pool called `default` [V: `agentapi.DefaultPool`, `sites.Manager.Create`, `hosting.Service.Create`], and that pool cannot be removed [V: `sites.Manager.RemovePool`].
- The pool name is stored twice: in the agent's registry (`sites.json`, pools and every site's pool) and in the panel's `sites.pool` column [V: `internal/sites/registry.go`, migration 0006]. Nothing on disk carries the name: the marker holds a random ID, unit files and drop-ins use the path [V: `sites.UnitIsolation`, marker in `pooladd.go`].
- Addendum 1 kept every pool change on the command line because add and remove carry a path. Rename and default carry only names.

### Decision (recommended)

1. **The registry gets a default.** `sites.json` gains an optional `default` field, written only when the default is not `default`, so an older agent still reads a registry whose default was never changed [V: test `TestRegistryDefaultField`]. A default that names no registered pool makes the registry invalid. A site without a pool lands on the registry's default; the panel asks the agent for it before it writes the site row.
2. **Two new typed agent operations without a path:** `pool.rename {name, newName}` and `pool.default {name}`. The agent accepts them from the panel as well as from root, since they change neither paths nor units. Rename changes the name of the pool and of every site on it in one atomic registry write; if the renamed pool was the default, the default follows.
3. **Panel API, admins only:** `POST /api/v1/pools/{name}/rename` with `{"name": NEW}` and `POST /api/v1/pools/{name}/default`, permission `pools:write` (token scope `system:write`), both in the audit log with the old and the new value. Rename changes the agent first and then the panel's sites. If the second step fails, sending the same rename again finishes it (the agent answers "unknown pool", the panel sees the new name and updates its rows).
4. **CLI:** `hostashi pool rename NAME NEWNAME` and `hostashi pool default NAME` go through the API like the other aliases of ADR-0058, so the panel's rows stay in step. `hostashi pool add -default NAME PATH` stays a root command and makes the new pool the default right away. `pool list` marks the default with a last column `default`.
5. **Remove** refuses the current default pool, whatever its name; another pool has to become the default first. The pool called `default` can be removed once it is no longer the default and has no sites.
6. **Panel page.** A page "Storage pools" under Server lists every pool with path, usage, free space, quota state, number of sites, availability and the default mark, with "Make default" and "Rename" for admins. The overview shows the pools' usage as well. Adding and removing stay on the command line (addendum 1).

### Consequences

- Positive: the default can move to a larger volume without naming the pool in every request, and names can be fixed after the fact without touching files or units.
- Negative: after a rename, scripts and API clients that use the old name must change. The audit log keeps both names. A downgrade to an agent before this addendum fails to read a registry whose default was changed [V: `DisallowUnknownFields` in `Registry.load`]; set the default back to `default` before a downgrade.
- Open: a rename that fails in the panel after the agent changed is finished by repeating it; there is no automatic repair [A: rare, the panel's step is one UPDATE on the local SQLite].
