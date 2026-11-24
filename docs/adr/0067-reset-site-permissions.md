# ADR-0067 Reset the file permissions of a site

**Status:** Proposed. Mode policy decided by Kim (2026-11-24, see question 1). **Date drafted:** 2026-11-24. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0016 (storage pools), ADR-0032 (SSH per site), ADR-0035 (roles), ADR-0040 (file manager), ADR-0054 Addendum 5 (deploy link), ADR-0058 (OpenAPI and CLI), ADR-0062 (Git deploy), [03-features](../03-features.md) "Linux user per site, home layout, permission reset" (P0).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- `03-features` lists a permission reset per site as P0 [V]. CloudPanel offers a reset for the site user; only the function is the reference here, not its modes or code (ADR-0001).
- A site home is `<pool>/<site>`, owned by the site user with the group `www-data` and mode `0750`; the web root `htdocs` is `site:site 0755` [V: `internal/sites/fs.go`]. Everything a site's processes write (PHP, cron, SSH, file manager, deploys, app units) is written as the site user [V: ADR-0032, ADR-0040, ADR-0061, ADR-0062].
- Wrong owners come from work as root: files copied in by an admin with `cp` or `rsync` as root, a restore by hand, an unpacked archive. Wrong modes come from uploads with `0777` or from programs that write `0600`, which then gives nginx a 403 for a static file [A: common support cases].
- The SSH keys of a site live in a root-owned file outside the home (`/etc/hostashi-ssh/keys/<site>`), so nothing in the home has to stay root's [V: ADR-0032].
- nginx refuses every path with a dot segment except `/.well-known/` [V: `internal/web/nginx.go`], so files such as `.env` are not served even when readable by `www-data`.
- The installer sets `fs.protected_hardlinks = 1` and `fs.protected_symlinks = 1` [V: `packaging/sysctl/90-hostashi.conf`], so a site user cannot hard-link a file of another owner into the home [U: kernel docs].
- The agent already walks homes on file descriptors only (`removeHome`, restore) with `openat2` and `RESOLVE_BENEATH|NO_SYMLINKS|NO_MAGICLINKS|NO_XDEV` [V: `internal/sites/fs.go`].
- The panel sits behind nginx with `proxy_read_timeout 60s` [V: `internal/web/nginx.go`].

## Options

**1. Modes**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Directories `0755`, files `0644`, files with any execute bit `0755`; setuid, setgid and sticky removed** | Fixes wrong owners, `0777` and too-tight files (403) in one step; scripts, `node_modules/.bin` and `.venv/bin` stay executable | Files a program made private (`0600`) become readable by `www-data` |
| B. Owner and group only, modes kept | Changes the least | Does not fix `0777` or 403 |
| C. Owner and group, then remove group/other write and setuid | Private files stay private | Does not fix 403 from too-tight files |

**2. How it walks**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Typed agent op in Go on file descriptors (`openat2` with `O_PATH|O_NOFOLLOW`, `fchownat`/`fchmodat2` with `AT_EMPTY_PATH`)** | No link is ever followed, no race between check and change, no other file system; no new `os/exec` exception | Own code (about 300 lines) |
| B. `chown -R` / `chmod -R` / `find` | Short | Needs an `os/exec` exception; `chown -R` follows links given on the command line, and races between `find` and `chmod` can be steered by the site user [U] |

## Recommendation

1A and 2A:

- New agent op `site.permissions.reset {site}`. The agent checks that the site is registered and that its Linux account still has the registry's uid and gid (else nothing is touched), opens the pool and the home without links, and walks it on fds.
- Home: owner the site user, group `www-data`, mode `0750` (as created). Every entry below it: owner and group the site user's.
- Directories `0755`, regular files `0644`, or `0755` if any execute bit was set. setuid, setgid and sticky are removed. Below the top-level `.ssh` and `.gnupg`, directories become `0700` and files `0600` (`0700` if executable), because ssh refuses private keys that others can read [U].
- Symbolic links get the site's owner on the link itself and are never followed; what they point to is reached, if at all, through its real path below the home. This keeps the Git deploy link `htdocs -> .deploy/releases/<n>` (ADR-0054 Addendum 5) as it is.
- FIFOs and sockets get the owner only; their mode stays as their program chose it.
- Skipped and reported, never changed: entries on another file system (bind mounts, other mounts inside the home), device files, and regular files with more than one hard link that belong to another owner (a guard where `protected_hardlinks` is off). The answer counts checked, changed and skipped entries and lists up to 50 skipped paths.
- Only what differs is written, so a second run only reads.
- Panel: `POST /api/v1/sites/{name}/permissions/reset` (action `sites.permissions:reset`, scope `sites:write`; every role for its own sites, like other work on the site's files). Not for sites without a Linux account (redirect, proxy). Audited as `site.permissions.reset` with the counts. The CLI gets it through `hostashi api sites reset-permissions NAME` (ADR-0058).
- The call stops after 50 s, below nginx's 60 s; a home that large has done part of the work and can be reset again, which then only reads what is already right.
- UI: a "Reset permissions" card in the site's settings with a confirmation dialog in which the site name is typed; it shows the result and the skipped paths.

## Consequences

- No new `os/exec` exception. The agent changes owners and modes as root, but only below the home it opened without links, on the same file system, and only to the site's own ids.
- Files that a program made private (for example `wp-config.php` with `0600`, or `.env`) become readable by `www-data` after a reset. Other site users still cannot enter the home (`0750`, group `www-data`) [V]. nginx does not serve dot files [V]; it would serve a `0644` file such as `htdocs/config.bak` that was `0600` before, but the same is true for every file a site writes with the usual umask [A]. Stated in the wiki.
- Running processes keep running; a file a program holds open keeps working, because owner and mode are checked on `open` [U].
- Project quota (ADR-0015) does not follow owners, so a reset does not move usage between quotas [U: ext4 project quota is per inode project id].
- Tests: unit tests for modes, idempotence, links to outside the home, the deploy link, a home swapped for a link, an account that changed or is gone, a missing pool, cancellation; a root test (in CI with sudo) for foreign owners, setuid, a hard-linked foreign file, a device node and a tmpfs mounted inside the home; API tests for roles, tokens and redirect sites.

## Open questions for Kim

1. ~~Modes: 1A, 1B or 1C?~~ Decided (Kim, 2026-11-24): 1A, directories `0755`, files `0644`, executable files stay `0755`.
2. Accept the rest of this ADR (walk on fds, `.ssh`/`.gnupg` private, skips, scope `sites:write`)?
