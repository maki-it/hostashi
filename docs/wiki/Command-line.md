# Command line

**For:** server admin. Every command runs on the server as root, for example with `sudo`, unless it says otherwise.

Most work is done in the panel. The command line is for what the panel cannot do for you: the first login, getting back in, checks and emergencies.

## Getting in

| Command | What it does |
|---------|--------------|
| `hostashi setup-token` | Prints the one-time token for the first login. |
| `hostashi user recover [-public-url URL] NAME` | Removes the password and second factors of account `NAME`, enables it and prints a new invitation link. See [Recovery](Recovery.md). |

## Checks

| Command | What it does |
|---------|--------------|
| `hostashi doctor` | Checks the server and every site without changing anything. Exit code `3` if a check failed, `0` otherwise. `-json` prints the result as JSON. |
| `hostashi ping` | Checks that the agent answers. |
| `hostashi version` | Prints the version. |

## Sites

| Command | What it does |
|---------|--------------|
| `hostashi site list` | Lists the site accounts. |
| `hostashi site files NAME` | Starts the file manager of a site and prints its socket. The panel does this by itself; the command is for checks. |
| `hostashi site limits NAME` | Shows the CPU, memory and process limits of a site. |
| `hostashi site limits NAME memory=BYTES tasks=N cpu-weight=N cpu-quota=PERCENT` | Sets them. A value you leave out means no limit. |

## Storage pools

A storage pool is an extra volume for site homes, for example a provider volume for large sites. Hostashi never formats or mounts a volume. You prepare it first:

1. Create an ext4 or XFS file system on the volume. For disk limits per site, create it with project quota: `mkfs.ext4 -O quota,project -E quotatype=prjquota /dev/...` (an existing, unmounted ext4 volume: `tune2fs -O project,quota -Q prjquota /dev/...`). XFS needs nothing at this step.
2. Add it to `/etc/fstab` with `nosuid,nodev,nofail` and, for disk limits, `prjquota`, for example `UUID=... /srv/vol1 ext4 nosuid,nodev,nofail,prjquota 0 2`, and mount it.
3. Leave it empty (`lost+found` is fine).

Without project quota a pool works, but its sites get no disk limit. The agent switches on the enforcement of the limits on every pool that counts usage, at its start and when a pool is added, so no `quotaon` is needed. `hostashi pool list` and `hostashi doctor` show the quota state of each pool: `enforced`, `counting` (limits are not enforced, doctor reports a failure) or `off`.

| Command | What it does |
|---------|--------------|
| `hostashi pool add NAME PATH` | Adds the mounted volume at PATH as pool NAME. It refuses, with the reason, a path that is not a mount point, not ext4 or XFS, not mounted with `nosuid,nodev`, not in `/etc/fstab`, not empty, inside a system directory or overlapping another pool. |
| `hostashi pool list` | Lists the pools with their path, number of sites and quota state. |
| `hostashi pool remove NAME` | Removes a pool without sites. Then you can unmount the volume. |

`hostashi doctor` fails a pool whose volume is not mounted or whose marker is missing, and warns when a pool's volume is mounted without `nosuid,nodev` or has no `/etc/fstab` entry. While a volume is missing, its sites do not start and cannot be changed; mount it and restart `hostashi-agent`.

A site gets its pool when it is created and stays there. Its databases stay in MariaDB's own directory.

## Databases

| Command | What it does |
|---------|--------------|
| `hostashi db list` | Lists the databases. |
| `hostashi db import DATABASE FILE` | Loads a dump into a database. `FILE` is relative to the site's home and ends in `.sql` or `.sql.gz`. |
| `hostashi db export DATABASE FILE` | Writes a dump of a database into the site's home. |
| `hostashi db user password USER` | Sets a new password for a database user. Reads it from standard input; an empty line generates one. |

## Backups

| Command | What it does |
|---------|--------------|
| `hostashi backup start` | Starts a backup now. |
| `hostashi backup status` | Shows the state of the last backup. |
| `hostashi backup restore SITE SNAPSHOT` | Restores a snapshot into a new folder `restore-<time>` in the site's home. Nothing existing is overwritten. |
| `hostashi backup restore-status ID` | Shows the state of a restore. |
| `hostashi backup key show` | Prints the backup repository password and the panel key. Store them outside the server: without them, backups cannot be restored on a new server. |

## Moving from CloudPanel

| Command | What it does |
|---------|--------------|
| `hostashi cloudpanel scan` | Run on the **old** CloudPanel server. Shows which sites can be moved, changes nothing. |

The rest of the move is done in the panel. A full guide follows.

## All commands

`hostashi help` prints every command, including the ones the panel and the system services use internally.
