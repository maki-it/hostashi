# ADR-0057 Database size and connection limits per site

**Status:** Accepted (Kim, 2026-11-22). **Date drafted:** 2026-11-22. **Decides:** Kim. **Related:** ADR-0015 (resource limits per site), ADR-0022 (metrics), ADR-0024 (MariaDB databases per site), ADR-0030 (database owner accounts), ADR-0038 (Adminer), ADR-0041 (external database servers), ADR-0044 (outgoing email).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- ADR-0015 recommendation 5 says: "connection limit per DB user by default, size shown and warned about, no automatic enforcement in v1" [V: `docs/adr/0015-per-site-resource-limits.md`]. None of the three exists yet: `CreateUser` creates site users without a resource limit, and the panel shows no database size [V: `internal/mariadb/mariadb.go`, `internal/serve/databases_api.go`]. Only the temporary Adminer users have `MAX_USER_CONNECTIONS 2` [V: `internal/mariadb/tool.go`, `internal/dbserver/dbserver.go`].
- The local databases live in `/var/lib/mysql`, owned by `mysql`. The site's project quota does not count them, so a growing database fills the server's disk past every site limit [V: ADR-0015 consequences "Database size cannot be hard-limited"].
- Warnings by email exist for site disk use, server disk and other checks; they read the doctor report hourly and send at 90 % [V: `internal/notify/watch.go`]. Site disk checks come with `warning` from 80 % and `critical` from 95 % [V: `internal/doctor/doctor.go` `Usage`].
- MariaDB knows `MAX_USER_CONNECTIONS` per account, set with `CREATE USER ... WITH` or `ALTER USER ... WITH`; 0 means no limit, and a login above the limit fails with error 1226 (`ER_USER_LIMIT_REACHED`) [U: MariaDB docs; checked for `CREATE USER` with the Adminer users in CI, V].
- information_schema reports `DATA_LENGTH` and `INDEX_LENGTH` per table. For InnoDB these are estimates from the statistics, not the size of the files [U]. Reading them for all registered databases took well under a second for 20,000 rows in a test [V: `TestServerSizes`, MariaDB 10.11].
- Decided (Kim, 2026-11-22): the warning threshold and the connection limit are values of the limit presets and of each site, like disk space.
- Decided (Kim, 2026-11-22): the size is only warned about, not limited. MariaDB has no size limit per database [U]. A project quota on `/var/lib/mysql/<db>` would make InnoDB fail writes at file level, which can stall the whole server and with it every site, and the shared logs are not counted [A]. Taking away the write privileges at the limit keeps the other sites safe but breaks the site at once (no logins, no orders) [A].

## Options

**Where the values come from**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Two new values in the limit presets and per site** | Same model as the other limits (presets are data, every value can be overridden per site), one panel page | Two more fields |
| B. One server-wide setting | Simple | A shop and a small brochure site get the same threshold |
| C. Only show the size, fixed connection limit | Least work | Nobody is warned before the disk fills |

**How the size is measured**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. `information_schema.TABLES`, data plus indexes, through the agent** | Standard, the same number Adminer shows, no file system paths | Estimate for InnoDB |
| B. Size of `/var/lib/mysql/<db>` on disk | What really fills the disk | Depends on the data directory layout and on `innodb_file_per_table`; undo and binary logs not attributable to a site |

## Recommendation

**Option A for both.**

1. **New limit values** `database_bytes` (warning threshold for the sum of a site's local databases) and `database_connections` (`MAX_USER_CONNECTIONS` of each database user of the site). 0 means unlimited, as for the other limits. Seeded values [E: guesses for typical WordPress sites, not measured]:

   | Preset | Database size (warning) | Connections per DB user |
   |--------|-------------------------|-------------------------|
   | small | 2 GiB | 20 |
   | medium | 5 GiB | 40 |
   | large | 10 GiB | 80 |

   The connection limit is well above the PHP workers of the preset (5 / 10 / 20), so cron jobs and SSH sessions fit next to them [A]. It is per account: a site with two database users can open twice as many connections [U].
2. **Measuring:** a read-only agent operation `db.sizes` returns the size of each database in the agent's registry. The panel caches the answer for five minutes and shows the size in the database list and on the database page (`sizeBytes` in `GET /api/v1/databases`, `/sites/{name}/databases`, `/databases/{db}`).
3. **Warning, no enforcement:** the size of a site's databases is compared with `database_bytes`: `warning` from 80 %, `critical` from 95 %, like disk use. The check appears on the health page as `site.database` and goes out by email at 90 % (ADR-0044 category "disk"), deduplicated like the disk warnings. Nothing stops a database from growing past the value.
4. **Connection limit:** a new agent operation `db.site.connections.set {site, max_connections}` runs `ALTER USER ... WITH MAX_USER_CONNECTIONS n` for every database user of the site in the agent's registry. The agent stores no limit of its own (an older agent after a rollback reads the same registry); the panel passes the site's value with every `db.user.create` (`max_connections`). The panel sends it together with the other limits (`applyLimits`), so a failure is recorded on the site; at start the panel applies every site whose limits are not applied yet, which also covers the sites the migration changed. A server without MariaDB is no error. Fixed statement, numeric value, users from the registry only: no new SQL surface, no new `os/exec` exception.
5. **Not affected:** the temporary Adminer and job users keep their own fixed limits, the locked owner accounts (ADR-0030) cannot log in anyway.
6. **External database servers (ADR-0041) are left out in v1.** Hostashi does not manage the storage or the `max_connections` of a server it does not run, managed services often refuse `ALTER USER ... WITH` resource options or meter storage themselves [U], and measuring would need a connection with information_schema rights to each server on every refresh. Their databases show no size.
7. **Existing sites:** the migration fills the new values of the seeded presets; sites that follow a preset get the preset's values, are marked as not applied and are applied at the next panel start. Custom sites keep 0 (unlimited) until an admin sets them, as ADR-0015 did for the other limits.

## Consequences

- Positive: the gap of ADR-0015 is closed with the same model and page as the other limits. One site's runaway script can no longer take all of MariaDB's connections [A].
- Negative: a site that legitimately needs more connections gets error 1226 until an admin raises the value; the panel shows the limit next to the users. InnoDB sizes are estimates, so the warning can be off by some percent [U].
- Not planned: a hard size limit (Kim, 2026-11-22, see Context).
- Implemented: #364 (`db.sizes`, sizes in the API), #368 (limit values, migration 0049, `db.site.connections.set`, panel), #371 (check `site.database`, email warning, wiki page Database limits).
