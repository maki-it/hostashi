# Spike S9 report: storage pools on a second volume

Status: spike, no product code changed. Evidence tags as in the ADRs: **[V]** verified by a test in this spike or by an existing CI job, **[C]** read from the code, not run, **[U]** unverified assumption, **[E]** estimate.

Question source: [ADR-0016](../../docs/adr/0016-per-site-storage-location.md), section "Spike S9". Code state: `main` at `c9e5ece` (2026-11-21).

## Setup

- `pool-test.sh` (this folder) installs Hostashi with `packaging/install.sh` in a Debian 13 container with systemd as PID 1, creates an ext4 file system on a loop device, mounts it at `/srv/vol1`, registers it as pool `vol1` and runs sites on it. It prints `RESULT` lines. Run as root on a host with docker: `spike/s9-storage-pools/pool-test.sh ./hostashi`.
- No real server, no Hetzner volume, nothing bought or changed outside the container and the loop device.
- Limits of the sandbox: its kernel has no `CONFIG_QUOTA`, so project quota on the second volume could not be switched on here. The container shares the host kernel, so a reboot with a missing volume could not be run either. Both are covered below from other evidence.

## How a pool gets added today

There is no way. The registry `/var/lib/hostashi-agent/sites.json` starts with the single pool `default` = `/home` [C: `sites.DefaultPools`]. No agent operation, CLI command, installer step or panel page adds one. For the spike the pool was written into the registry by hand (agent stopped, JSON edited, agent started). The agent accepted it [V].

## Results

| # | ADR-0016 question | Result |
|---|-------------------|--------|
| 1 | Do marker and mount check stop writes to an unmounted pool? Does `RequiresMountsFor` stop the units? | **Agent: yes [V]. Units: no, nothing sets it [V].** Details in findings 1 to 4. |
| 2 | Project quota on a second volume, enabled on a fresh volume? | **Yes in principle [V], but the installer and the enforce unit only know `/` [C].** Finding 6. |
| 3 | Cross-filesystem copy, 50 GB with a live first pass | **Not tested.** Moving sites is a later step (Kim, ADR-0016); out of this spike. |
| 4 | Copy implementation without shell strings | **Not tested**, same reason. |
| 5 | Path validation against symlink and bind-mount tricks | **Holds at use time [V]; no validation at registration, because there is no registration [C].** Finding 5. |

### What already works [V]

- Site `big` created through the API with `"pool":"vol1"`: home `/srv/vol1/big` (mode 750 `big:www-data`) on `/dev/loop0`, `/etc/passwd` points there, PHP-FPM unit runs with `BindPaths=/srv/vol1/big`, nginx serves PHP from it.
- Cron job runs with working directory `/srv/vol1/big`. File manager lists and writes the home. Page cache directory lands on the volume (`/srv/vol1/.hostashi-cache`, 711 root).
- A site on `/home` (`small`) cannot list `/srv/vol1` or `/srv/vol1/big` as its user (pool 0711, home 0750).
- `hostashi doctor` lists free space, quota state and mode for each pool.
- Volume really unmounted with the agent restarted (like a boot without the volume): `site create t2 vol1` and `site delete t1` both refuse with "storage pool unavailable", the account of `t1` stays, nothing is written below the empty mountpoint.
- Pool path is a symlink, marker ID wrong, or a plain directory with a correct marker that is not a mount: all three refused with "storage pool unavailable".

## Findings

1. **Isolation gap: PHP masters see the other pools [V].** `ProtectHome=tmpfs` in `hostashi-php@.service` and `hostashi-files@.service` only hides `/home`, `/root` and `/run/user`. The PHP master of `small` (pool `default`) has `/srv/vol1` with `.hostashi-pool` and `big` in its mount namespace, down to `/srv/vol1/big/htdocs/index.php` (seen through `/proc/<pid>/root` as root). The workers still run as the site user and are stopped by the file modes, but the guarantee of ADR-0033 ("the master never sees another site's home") no longer holds once a second pool exists. Fix verified with `systemd-run`: `TemporaryFileSystem=<pool>:ro` for every non-default pool hides it, and with `BindPaths=<home>` on top the site sees only its own home and can write there [V].
2. **No `RequiresMountsFor` [V].** The PHP unit of `big` has `RequiresMountsFor=/run/hostashi-php/big` only, and no ordering after the volume's mount. Verified with `systemd-run` [V]:
   - without an fstab entry, `RequiresMountsFor=/srv/vol1/t1` does **nothing** when the volume is missing: the unit starts;
   - with an fstab entry (`nofail`) and the device missing, the unit does **not** start (it waits for the device and fails).
   So the drop-ins (PHP, cron, file manager, DB jobs, restore, import) need `RequiresMountsFor=<home>`, and a pool must have an fstab entry, which adding a pool should check.
3. **Page cache can write to the root disk below an unmounted pool [C].** `pagecache.Dirs.openRoot` opens the pool path and creates `.hostashi-cache` with `mkdirat` without the mount and marker check that `sites.openPool` does. With the volume missing, turning the cache on would create the cache tree on the root file system below the empty mountpoint. Not triggered in the run (the unit failed first), so [C], not [V]. Fix: open the pool through the same check.
4. **Doctor does not see an unmounted pool [V].** With `/srv/vol1` unmounted, `system.disk` reports the free space of the root disk for `/srv/vol1`, and there is no check that the pool is mounted and carries its marker. Needed: a `system.pool` failure "not mounted / marker missing", a warning without `nosuid,nodev`, and one without an fstab entry.
5. **No validation of pool paths at registration [C, V].** Two pools on the same path were accepted by the agent [V]. The rules of ADR-0016 finding 3 (absolute, clean, not under system trees, not nested in or containing another pool, ext4 or XFS, root 0711) exist only partly as checks at use time. They belong into the operation that adds a pool. Side note: the empty mountpoint directory below the volume was mode 0777 in the container [V]; a site user could write there while the volume is missing. Adding a pool should set the bare mountpoint to root 0755 or stricter when it is unmounted, or document it [U whether a real server shows the same].
6. **Quota on a second volume [V, C].** The quota code works per directory with `quotactl_fd`, so it works on any ext4 mount: the CI job "Project quota (ext4 on a loop device)" already runs the quota tests against `/mnt/quota`, a non-root loop volume [V]. `tune2fs -O project,quota -Q prjquota` on the unmounted fresh volume worked without a reboot [V], unlike the root file system (spike S8). But: `hostashi-quota-enforce.service` runs only `quotaon -P /` [C], and `quota.Available` reports "available" as soon as accounting is on, even when limits are not enforced (S8 finding 3) [C]. On a second volume mounted with `prjquota`, limits would be counted but not enforced, silently. Needed: enforcement per pool (template unit or the agent) and a doctor check that tells "enforced" from "counting only".
7. **Backups and restore follow the pool [C].** The backup reads homes from the registry (`sites.ReadHomes`), so the path is the pool path. With the volume missing, `checkDir` fails that site's files item, the other items are saved and the run shows as needing attention; no empty snapshot replaces the good ones. Restore writes into the home on the pool and checks free space through the quota of that file system. Not run end to end in the spike (no SFTP target set up) [U].
8. **Import always uses `default` [C].** `cpmigrate.CreateOptions` and the migration API have no pool, so imported sites land in `/home`. Needed: a pool choice per site in the import plan, with the space check against that pool.
9. **Panel and API [C].** `POST /api/v1/sites` already takes `pool` and validates the name; the site page shows the pool. There is no list of pools in the API and no pool picker in "New site".
10. **A lazy unmount is not a missing volume [V].** In the container, `umount -l` on the host side did not reach the agent's mount namespace (container root is not a shared mount), so the agent kept working on the volume. On a real server systemd makes `/` shared and services are slaves, so an unmount should propagate [U]. The spike therefore tested "missing volume" by stopping the units, unmounting and starting the agent again.

## Proposal: what is needed to add pools

Small PRs in this order. Everything stays inside ADR-0016; an addendum there records the details (no new ADR number needed [U]).

1. **Hardening first (no new feature):** `TemporaryFileSystem=` for the non-default pools and `RequiresMountsFor=<home>` in every site drop-in; page cache opens the pool with the mount and marker check. Tests in `container-test.sh` with a loop volume.
2. **Add and remove pools, root only:** typed agent operations `storage.pool.add` / `storage.pool.remove` and `hostashi pool add NAME PATH`, `pool list`, `pool remove NAME`. Checks of ADR-0016 finding 3 plus: mount root, ext4 or XFS, fstab entry, `nosuid,nodev`, not a duplicate or nested path. Writes the marker, sets 0711. Remove only when no site uses the pool. Not in the panel: adding a pool touches mounts and fstab, which needs root on the server anyway [U].
3. **Quota per pool:** enforcement for each pool with quota, `quota.Available` tells enforced from counting, and an optional `hostashi pool add --enable-quota` that sets the features on the still unmounted fresh volume (no reboot needed, verified). Hostashi never formats or partitions.
4. **Doctor:** per pool "mounted with marker", fstab, mount options, quota enforced.
5. **Panel and API:** `GET /api/v1/pools` (name, free space, quota, state; admin), pool picker in "New site" when more than one pool exists. Screenshots before the PR.
6. **Import:** pool per site in the import plan.
7. Later, as in ADR-0016: moving a site between pools (questions 3 and 4 of this spike).

Estimate for steps 1 to 6: 4 to 6 PRs, about 3 to 5 days [E].

## Not covered

- A reboot with a missing volume on a real systemd host (fstab `nofail`, `RequiresMountsFor`), XFS, project quota enforcement on a second volume on a real kernel, a real Hetzner volume. A CI job can cover quota on a second loop volume; the reboot needs a VM [U].
- Cross-filesystem copy and moving sites (questions 3 and 4).
