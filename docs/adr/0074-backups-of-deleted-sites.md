# ADR-0074 Backups of deleted sites

**Status:** Proposed. **Date drafted:** 2026-11-26. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0008 (backups with restic), ADR-0018 (REST API, scope `backups:restore`), ADR-0035 (roles), ADR-0058 (OpenAPI and CLI).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim reported (2026-11-26): after a site is deleted, its snapshots can no longer be reached on the Backups page.
- The page offers only the sites that exist in the panel in its site selector, and lists the snapshots of the chosen site [V: `web/src/Backups.tsx`]. The API `GET /api/v1/sites/{name}/backups` already answers for any valid name, also a deleted one, because the agent filters `snapshots.json` by the `site:` tag [V: `internal/backup/setup.go` `Snapshots`].
- Deleting a site does not touch the backup repository [V: grep over `internal/`]. Its snapshots stay.
- Retention runs `restic forget --group-by host,tags` with keep counts per group [V: `internal/resticctl` `Forget`]. `--keep-daily 7` keeps the last 7 days *that have a snapshot*, not the last 7 calendar days [U: restic documentation], so a group that gets no new snapshots is never emptied. The snapshots of a deleted site therefore stay until someone removes them by hand: today at most daily + weekly + monthly + yearly + hourly snapshots per kind, deduplicated against the rest of the repository.
- A restore writes into `<home>/restore-<time>` of the same site and a dump goes into a new database of the same site [V: ADR-0008 recommendation 7, `internal/backup/restore.go`]. The agent looks the home up in its site registry; a deleted site has none, so the restore fails with "snapshot not found" [V: `siteHome`].
- Site names can be reused. A new site with the name of a deleted one sees the old site's snapshots today, because snapshots are matched by name only [V: same code]. With one administrator this is a surprise; with a site user (ADR-0035) who is a different customer it shows data of someone else [A].
- Deleted data may have to be erased on request (for example a customer leaving under GDPR Art. 17) [A, not legal advice]. There is no way to do that in the panel today.

## Options

### Where a snapshot of a deleted site is restored to

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Into an existing site the administrator picks** (often a site just created again under the same name), as `restore-<time>` and a new database of that site | Reuses the restore path that exists and is hardened (unit per restore, `openat2` without symlinks, space check); no new data path | Data of one site lands in another; must be limited to administrators |
| B. Download as an archive in the browser | No target site needed | New streaming path agent → web → browser for possibly many gigabytes, the agent has no network, a new unit; large change |
| C. Both, A first | Covers both needs | B later as its own step |

### How long the snapshots of a deleted site stay

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Until the administrator deletes them** with a "Delete backups" action (typed name confirmation), listed under "Deleted sites" | Nothing is lost by surprise; erasure on request becomes possible | Space is used until someone acts |
| B. Expire automatically N days after deletion | No manual work | Needs the deletion time stored outside the panel database, and data disappears without a person deciding |
| C. Leave as today | No work | No erasure possible, snapshots stay forever |

### Name reuse

| Option | Pro | Contra |
|--------|-----|--------|
| **A. A site shows only snapshots taken after it was created; older ones with the same name count as a deleted site** | A new owner never sees the previous owner's data | Relies on the creation time of the site; snapshots of the same site created before this ADR are unaffected because they are newer than the site [A] |
| B. Keep matching by name only | No work | Leaks the previous site's data to a new site user |

## Recommendation

Options **A, A, A**.

1. **Listing.** A new agent operation `backup.sites` returns every site name that has snapshots in `snapshots.json`, with the count, the newest time and the oldest time. The web process marks a name as *deleted* when no site of that name exists, and as *earlier site* for the snapshots of an existing name that are older than the site's `created_at`. New route `GET /api/v1/backups/sites` (scope `backups.server:read`, administrators only) through `mux.Op`.
2. **Browsing.** The Backups page gets a "Deleted sites" group in the site selector (administrators only), with the same snapshot table. `GET /api/v1/sites/{name}/backups` keeps answering for existing sites and only returns snapshots newer than the site's creation; the deleted ones are reached through `GET /api/v1/backups/deleted/{name}` (`backups.server:read`), which also returns the snapshots of an earlier site of a reused name.
3. **Restore.** `BackupRestoreRequest` gets an optional `Target` site. The agent accepts a target other than the snapshot's site only when the snapshot's site has no entry in its registry (deleted) or the snapshot is older than the target's registry entry (earlier site); the target must exist. Everything else stays as in ADR-0008 recommendation 7: `restore-<time>` in the target's home, dumps into a new database of the target. Route `POST /api/v1/backups/deleted/{name}/{snapshot}/restore` with body `{"target": "<site>"}`, scopes `backups:restore` and `backups.server:write`. The audit log records source, target and snapshot.
4. **Deleting.** "Delete backups" removes every snapshot tagged `site:<name>` of a deleted site (or of an earlier site, up to its creation time) with `restic forget <ids>` in a unit like the restore, because the agent has no network. The panel asks to type the site name, shown highlighted and copyable. Space is freed by the next weekly `prune`. This step is irreversible and high-risk (`backups.server:write` plus a fresh login as for other high-risk actions). It comes as its own PR after 1 to 3.
5. **Not in this ADR:** download as an archive (option B of the first table) and automatic expiry; both can follow as later ADRs.

## Consequences

- One new agent operation (`backup.sites`), one optional field on `backup.restore`, later one operation `backup.forget` with its unit. No new `os/exec` exception: restic stays in `internal/resticctl`.
- The snapshot list of an existing site can become shorter after an update, where a name was reused; those snapshots move to "earlier site".
- Wiki: the backup page explains deleted sites, restore into another site and deleting backups.
- Site users (ADR-0035) see nothing new; everything under "Deleted sites" is for administrators.

## Open questions for Kim

1. ~~Where is a snapshot of a deleted site restored to?~~ Decided by Kim (2026-11-26): A, into an existing site the administrator picks.
2. How long do the snapshots of a deleted site stay? Recommendation: A, until the administrator deletes them.
3. Name reuse: recommendation A, a site shows only snapshots taken after it was created.
