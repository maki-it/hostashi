# ADR-0070 Staging and cloning a site

**Status:** Proposed. **Date drafted:** 2026-11-25. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0008 (backups), ADR-0015 (limits and quota), ADR-0016 (storage pools), ADR-0024 and ADR-0025 (MariaDB per site, import and export), ADR-0028 (cron), ADR-0032 (SSH per site), ADR-0035 (roles), ADR-0041 (external database servers), ADR-0045 (object cache), ADR-0046 (page cache), ADR-0054 (site types), ADR-0056 (security headers), ADR-0062 (Git deploy), ADR-0066 (password protection), ADR-0067 (permission reset), ADR-0069 (profiles and WP-CLI, drafted with this one), the ADR for changing the domain of a site (drafted in parallel), [03-features](../03-features.md) row "Staging/cloning a site" (P1, in 1.0 scope).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- 1.0 contains "Staging/cloning a site", note "Files + DB, domain rewrite" [V: `03-features.md` 3.0 and 3.1].
- A site is a fixed id and a name (its Linux user), a home `<pool>/<site>`, domains, certificates, databases on the local MariaDB, and optional parts: app unit, cron jobs, SSH keys, Git deploy, object cache, page cache, access rules, security headers [V: `internal/store/sites.go`, ADRs above].
- Databases live in `/var/lib/mysql` and do not follow storage pools; the site quota does not count them [V: project notes, ADR-0016]. Database and user names are free, not prefixed with the site (ADR-0024) [V].
- `mariadbctl` already dumps and loads databases as the site user with fixed arguments (ADR-0025), and `dbjob` does the same for external servers in a unit [V: `.golangci.yml`, `internal/dbjob`].
- The agent walks homes on file descriptors (`openat2` with `RESOLVE_BENEATH|NO_SYMLINKS|NO_MAGICLINKS|NO_XDEV`) for the permission reset and for removal [V: ADR-0067, `internal/sites/fs.go`]. ADR-0016 sketches the same kind of typed copy routine for moving a site between pools, still unbuilt [V].
- A plain SQL `REPLACE()` of a URL breaks PHP-serialized values, whose string lengths are stored next to them; WordPress keeps options and widget data that way [U]. WP-CLI's `search-replace` unserializes, replaces and serializes again [U].
- A clone that runs the live site's cron jobs or its mail settings can send real mails, charge cards or call webhooks a second time [A: common staging accident].
- Hostashi has no mail server (scope), so it cannot catch outgoing mail of a clone; applications send through their own SMTP settings [V: scope v1].

## Options

**1. What the feature is**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Clone only: a one-time copy into a new, independent site | Simple, no link between the sites | "Staging" is just a name; refreshing means delete and clone again |
| **B. Clone, optionally marked as staging of its source, with "refresh from source"** | The common workflow (copy live, test, refresh) without merge problems; the mark drives safe defaults and the UI link | Some extra state (source id) and a refresh flow |
| C. B plus "push to live" (files and database back) | Full staging workflow | Database merge is unsolved: orders, comments and users written on live in the meantime would be lost [A]; easy to destroy a live site |

**2. How files are copied**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Typed copy in the agent on file descriptors, into the new home, owners mapped to the new site user** | Same safety as the permission reset (no link followed, no other file system); no new `os/exec` exception; reusable for the pool move of ADR-0016 | Own code; ACLs and xattrs only if we add them |
| B. Restore the latest restic snapshot into the new site | Reuses the restore path | Needs a backup; snapshot can be a day old; slower |
| C. `cp -a` or `rsync` through `os/exec` | Short | New exception; link handling of a root `cp` in a user-controlled tree is risky [U] |

**3. Rewriting URLs in the database**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. WordPress only, through WP-CLI `search-replace` (ADR-0069 runner) as the clone's user; other applications get a checklist** | Correct for serialized data [U]; uses the same runner and pinned WP-CLI | Laravel, Shopware and the rest need a manual step |
| B. Own Go search-replace aware of PHP serialization, for every application | Works without WP-CLI | Own parser for an untrusted format on the hot path of a copy; hard to get right |
| C. No rewrite at all | Nothing to build | WordPress clones redirect to the live domain and are useless without manual SQL [U] |

## Recommendation

1B, 2A and 3A.

**Flow**

- `POST /api/v1/sites/{name}/clone` with the new site name, its first domain, the target pool, and "mark as staging". Admins and managers (like creating a site, scope `sites:create`) with read access to the source. Accounts with a grant on the source get the same grant on the clone.
- The panel checks the name, the domain (not used by any site or parked domain), free space on the target pool (source home plus 10 % [E]) and, for the databases, free space on `/var/lib/mysql`. Only sites with a Linux account can be cloned (not redirect or proxy sites).
- The clone is created like a new site with the source's type, profile (ADR-0069), PHP version and settings, limits preset, security headers and web settings. Then the agent copies the home (2A), skipping the site's `.ssh` and the Git deploy data that belongs to the source (deploy key, credentials); release folders and the deploy link are copied as they are.
- Databases of the source on the local MariaDB are dumped with `--single-transaction` as the source user and loaded into new databases as the clone's user (through `mariadbctl`). New names default to `<name>_<suffix>` with a suffix from the clone's name, cut to the ADR-0024 limits; new users and passwords are generated. Databases on external servers (ADR-0041, ADR-0051) are not cloned in 1.0; the UI says so.
- Config files: for WordPress the runner sets `DB_NAME`, `DB_USER`, `DB_PASSWORD` with `wp config set`, then runs `search-replace` from every source domain (with and without `www`, `http` and `https`) to the clone's domain, then `cache flush`. For Laravel and Symfony the agent changes the `DB_*` and `DATABASE_URL` keys in `.env` as typed key edits. Other applications get a checklist of what to change.
- The copy of a running site is not a snapshot: files changed during the copy can be inconsistent [A]. An option "put the source in maintenance during the copy" (ADR-0027) exists, off by default.
- Status and log like a deploy; on failure the half-made clone is deleted with its databases.

**What a staging clone gets by default**

- Cron jobs copied but disabled; the person enables them one by one.
- Password protection on (ADR-0066) with a generated user and password shown once, and `X-Robots-Tag: noindex, nofollow` on every response.
- For WordPress: `WP_ENVIRONMENT_TYPE` set to `staging` [U: some plugins then hold back mails or payments]; the UI warns that the application's own mail settings still send real mail.
- Page cache off, object cache a new empty instance, Git deploy and webhook not copied, SSH keys not copied, app environment variables copied (they may contain live API keys; the UI says so).
- A certificate is requested for the clone's domain once DNS points to the server; until then it works on HTTP behind its password.

**Refresh from source**

- On a staging clone, "Refresh from source" copies files and databases again into the same clone (same name, domain, users, password protection), after the name of the clone is typed to confirm. The clone's own files and data are replaced.

## Consequences

- No new `os/exec` exception beyond the WP-CLI runner of ADR-0069; if ADR-0069 does not choose WP-CLI, option 3A falls away and this ADR needs 3B or 3C.
- The typed copy routine is new agent code that runs as root over a tree the site user controls. It gets the same tests as the permission reset (links, a home swapped for a link, other file systems, device files, hard links to foreign files) plus a cancellation test, and it becomes the base for moving sites between pools later.
- Two databases of the same size double the use of `/var/lib/mysql`, which no site quota covers; the panel shows the database size before the clone [V: sizes exist from ADR-0057].
- No push to live in 1.0. A later ADR can add "copy files to live" without the database.
- Effort [E]: copy routine and agent op about 2 PRs, database copy and config edits 2 PRs, WordPress rewrite with the ADR-0069 runner 1 PR, API, UI, refresh and wiki 3 PRs.
- Tests: copy unit and root tests, database copy against MariaDB in CI, an installer test that installs WordPress (ADR-0069), clones it and loads the clone's front page under its domain without a redirect to the source.

## Open questions for Kim

1. Clone with an optional staging mark and "refresh from source" (1B), or also "push to live" (1C)? Recommendation: 1B; pushing a database back loses data written on live in the meantime.
2. Safe defaults for a staging clone: cron disabled, password protection on and `noindex`? Recommendation: yes, all three, each switchable afterwards.
3. Rewrite URLs only for WordPress with WP-CLI and give other applications a checklist (3A)? Recommendation: 3A; a generic rewrite of serialized data is error-prone.
