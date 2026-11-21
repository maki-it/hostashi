# ADR-0051 External PostgreSQL servers

**Status:** Accepted (Kim, 2026-11-21). **Date drafted:** 2026-11-21. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0008 (backups), ADR-0024 (MariaDB per site, no stored passwords), ADR-0025 (import and export), ADR-0033 (hardening), ADR-0035 (roles), ADR-0038 (web database tool), ADR-0041 (external database servers and its Addendum 1), [`03-features.md`](../03-features.md) row "PostgreSQL" (P1).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants one local database engine only: MariaDB stays, no local PostgreSQL [V: Kim, thread, 2026-11-21].
- For sites that need PostgreSQL, Kim wants external PostgreSQL servers, and wants them **in v1** with the same features as external MariaDB/MySQL servers [V: Kim, thread, 2026-11-21].
- ADR-0041 already built external servers for MariaDB and MySQL: server list `db_servers` with a sealed admin password and TLS modes `verify`, `verify-ca`, `none`; databases point at a server; the panel process talks to the server; backups dump through `hostashi-backup`; import and export run in `hostashi-dbjob@<id>`; Adminer reaches external servers through an `IPAddressAllow=` drop-in [V: ADR-0041, `internal/dbserver`, `internal/dbjob`, `internal/hosting/dbremote*.go`].
- The table `db_servers` has an `engine` column; `internal/dbserver` knows only `MariaDB` and `MySQL` [V: `internal/store/migrations/0029_db_servers.sql`, `internal/dbserver/dbserver.go`].
- `os/exec` is allowed only in five packages; `mariadb` and `mariadb-dump` run through `internal/mariadbctl`, and restic starts `mariadb-dump` through `internal/resticctl` [V: `.golangci.yml`].
- The pinned Adminer file is the MySQL-only build `adminer-6.3.1-mysql-en.php` [V: project memory of the Adminer thread; SHA-256 in `packaging/adminer/SHA256SUMS`]. Adminer itself supports PostgreSQL through PHP's `pgsql` extension [U].
- Go driver: `github.com/jackc/pgx/v5`, latest v5.13.0 (2026-11-11), MIT [V: proxy.golang.org; license U].
- `pg_dump` can dump servers of its own major version and older, not newer ones [U]. Debian 13 and Ubuntu 26.04 each ship one PostgreSQL major [U: which one is not checked]; the PostgreSQL project's own apt repository (apt.postgresql.org) ships all supported majors for both [U].

## Options

### How the engines differ for Hostashi

| Topic | MariaDB/MySQL (today) | PostgreSQL |
|-------|-----------------------|------------|
| Admin rights | `CREATE`, `DROP`, `CREATE USER`, `GRANT OPTION` | `CREATEDB` and `CREATEROLE`, no superuser [U] |
| Database user | `'user'@'<client host>'` with grants on one database | role with `LOGIN`, owner of its database; host limits live in the server's `pg_hba.conf`, not in SQL [U] |
| Isolation | grants per database | `REVOKE CONNECT ... FROM PUBLIC` on each database, schema `public` owned by the site role [U] |
| Dump and load | `mariadb-dump`, `mariadb` | `pg_dump` (custom format), `pg_restore`; `psql` for plain SQL files [U] |
| End a temporary user | `DROP USER` (MariaDB also `KILL ... USER`) | `pg_terminate_backend` for its sessions, then `DROP ROLE` [U] |

### Where the client tools come from

| Option | Pro | Contra |
|--------|-----|--------|
| **A. `postgresql-client-<major>` from apt.postgresql.org, pinned key, installed only when the first PostgreSQL server is added (same pattern as sury.org, ADR-0019/0036)** | One client that dumps every supported server version [U] | A further third-party apt source with root installation |
| B. The distribution's `postgresql-client` | No new source | Cannot dump a server newer than the distribution's major [U]; managed offers move fast |

### Adminer

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Switch to Adminer's full English build (all drivers) and install `php<version>-pgsql` for the tool's pool** | One file, one pool, one theme | Larger file, drivers we do not use stay inside [U] |
| B. A second Adminer file and pool only for PostgreSQL | Smallest change for the MySQL tool | Two pools, two units, the theme twice |

## Recommendation

**Option A in both cases.** External PostgreSQL servers become a third engine in the ADR-0041 design. Everything else stays as it is for MariaDB/MySQL: server list, sealed admin password, TLS modes with the warning for `none`, default server, version shown in the list, the 15-minute check, roles and scopes (`db_servers:write` admin-only).

- **Engine.** The "Add server" dialog asks for the engine (MariaDB/MySQL or PostgreSQL). `internal/dbserver` gets a PostgreSQL implementation behind the same typed operations, with pgx and `sslmode` `verify-full` (`verify`), `verify-ca` with the uploaded CA, or `disable` (`none`). Supported: PostgreSQL 14 and newer [A: the versions still supported upstream at release time].
- **Privilege check when adding.** The admin role needs `CREATEDB` and `CREATEROLE`; Hostashi accepts a superuser role but warns and recommends a dedicated role [A]. On PostgreSQL 16 and newer a `CREATEROLE` role manages only the roles it created [U], which fits "Hostashi manages only what it created".
- **Databases.** For each database: a role `<name>` with `LOGIN` and a random password (shown once, never stored, ADR-0024), `CREATE DATABASE <name> OWNER <role>`, `REVOKE CONNECT ON DATABASE <name> FROM PUBLIC`, and inside the database `ALTER SCHEMA public OWNER TO <role>`. Names follow the existing strict validator; identifiers are always quoted by the code, passwords are sent as a parameter-free literal like today. Delete: terminate the role's sessions, `DROP DATABASE`, `DROP ROLE`.
- **Client host.** PostgreSQL has no host part in its roles; access by host is the server's `pg_hba.conf`. The server dialog says so and recommends allowing only this host's address [A].
- **Backups.** `hostashi-backup` dumps external PostgreSQL databases with `pg_dump --format=custom` over TLS with the admin credential, as for external MariaDB. restic starts `pg_dump` like it starts `mariadb-dump`, so `internal/resticctl` names one more fixed binary. Restore uses `pg_restore --clean --if-exists --no-owner --role=<site role>`.
- **Import and export.** `hostashi-dbjob@<id>` runs `pg_dump`/`pg_restore` (custom format) or `psql` for `.sql` files as the site user, with a temporary role `hostashi_job_<16 hex>` that is granted the site role and has `CONNECTION LIMIT 2` and `VALID UNTIL`. `psql` runs with `ON_ERROR_STOP`, without `.psqlrc` (`-X`), and with meta-commands refused [A: psql cannot switch them off entirely; the file runs with only the temporary role's rights, which is the actual guard].
- **New `os/exec` exception.** `internal/pgctl` runs `/usr/bin/pg_dump`, `/usr/bin/pg_restore` and `/usr/bin/psql` (the versioned binaries from the chosen major) with fixed arguments, the password through a 0600 passfile in the job's private temp directory, never through the environment or the command line. This is the sixth exception; it needs this ADR's acceptance.
- **Adminer.** The tool pool switches to Adminer's full English build and loads `pgsql`. The wrapper `index.php` takes the engine from the ticket and accepts only `server` or `pgsql`. The temporary role `hostashi_tool_<16 hex>` gets the site role, `CONNECTION LIMIT 2` and `VALID UNTIL` the session end; ending a session terminates its backends and drops the role. Extending and ending sessions (PR #305) work the same way.
- **Client packages.** The installer does not install PostgreSQL tools by default. Adding the first PostgreSQL server installs `postgresql-client-<major>` of the newest major from apt.postgresql.org through the existing fixed root unit for package installs (ADR-0036 pattern, `phpctl`), with the key pinned by fingerprint. A server newer than the installed client is refused with a clear message until the client is updated [A].
- **UI.** The database list shows the engine; "New database" offers PostgreSQL servers only when one exists; the connection hint for a PostgreSQL database shows host, port, database, user and `sslmode`.
- **doctor.** Reports unreachable PostgreSQL servers, a client older than a server, and leftover `hostashi_tool_`/`hostashi_job_` roles.

## Consequences

- Migration: none for the server table (`engine` exists) [A: the store may need a check constraint update]; possibly one for the engine of `databases` if it is not derived from the server. The number comes from the coordinator.
- New package `internal/pgctl`, PostgreSQL code in `internal/dbserver`, a PostgreSQL path in backups, jobs and the Adminer tool.
- CI: the `External DB server` job gets PostgreSQL images (each supported major) with TLS `verify-ca` and `none`; the container test adds a PostgreSQL container as an external server and runs create, backup, restore, import, export and Adminer against it.
- Adminer's pinned file and hash change to the full build.
- Estimate: 6 to 8 PRs [E].

## Risks to name openly

- **More attack surface.** A sixth `os/exec` exception, a further third-party apt source, and Adminer's PostgreSQL driver code in the tool pool.
- **Stored admin credential** of a PostgreSQL server, as in ADR-0041; a dedicated role with `CREATEDB`/`CREATEROLE` only keeps the damage smaller.
- **`psql` runs SQL files with client-side meta-commands.** The temporary role, the job sandbox and the site user limit what a crafted file can do; it is not a guarantee [A].
- **Version drift.** Managed PostgreSQL offers update majors on their own schedule; an outdated client stops backups of that server until the client is updated. doctor and "Needs attention" must make this visible.
- **Second dialect everywhere.** Every database feature now has two code paths and two test paths.
- **Not a pentest.**

## Open questions

1. ~~External PostgreSQL servers in v1?~~ **Decided (Kim, 2026-11-21): yes, directly in v1, with the full feature set (databases, backups, import and export, Adminer).**
2. ~~Client tools from apt.postgresql.org (option A) instead of the distribution's client?~~ **Decided (Kim, 2026-11-21): yes, from the official repository.**
3. ~~Adminer: switch to the full build in one pool (option A)?~~ **Decided (Kim, 2026-11-21): yes.**
4. ~~The new `os/exec` exception `internal/pgctl` for `pg_dump`, `pg_restore` and `psql`?~~ **Decided (Kim, 2026-11-21): yes.**

## Implementation notes

Details settled while building step 1 (`internal/dbserver/postgres.go`, 2026-11-21). They refine the recommendation above; they do not change a decision.

- **Owner role per database.** Hostashi allows several users per database, so a database is owned by a group role `hostashi_db_<16 hex of SHA-256(name)>` without `LOGIN`, not by the first user. Each user is a `LOGIN` role, member of that group, with `ALTER ROLE ... SET role` to it, so all users of a database share its objects as on MariaDB [V: integration test on PostgreSQL 16.15]. The hash keeps the name within PostgreSQL's 63 bytes; site users cannot take names starting with `hostashi`.
- **No `ALTER SCHEMA public OWNER`.** On PostgreSQL 15 and newer `public` belongs to `pg_database_owner`, which is the owner role [U]; on 14 a non-superuser cannot change it, and `public` stays open to the database's members only because `CONNECT` is revoked from `PUBLIC` [U, checked by the CI job on 14].
- **Admin membership.** The administrator grants itself every role it creates (`GRANT ... TO CURRENT_USER`). PostgreSQL 16 and newer need that to create a database for another owner and to end a role's sessions [U, checked by CI on 14 to 19].
- **Passwords as SCRAM verifiers.** The panel sends `PASSWORD 'SCRAM-SHA-256$4096:...'`, computed in Go, never the plain password, so even `log_statement = 'all'` logs no password [V: server log in the integration test].
- **Deleting a user** turns off its login, ends its sessions, moves objects it made after `RESET ROLE` to the owner role (`REASSIGN OWNED`, in that database) and drops it. Deleting a database uses `DROP DATABASE ... WITH (FORCE)` and drops the owner role.
- **Names.** On PostgreSQL, databases `postgres`, `template0`, `template1`, names starting with `pg_` and database names over 63 characters are refused; so are the role names `public`, `none`, `postgres`, `user`, `current_user`, `current_role` and `session_user`.
- **Maintenance database.** The administrator connects to `postgres` [A: every server has it]. The pgx configuration ignores `PG*` environment variables, `~/.pgpass` and service files.
- **Until the later steps land**, backups list PostgreSQL databases as failed with the reason, and import, export and the web database tool answer `postgresql_not_yet`.
- **Client packages (PR #316) [V: local run and container test].** `packaging/postgresql/pg-client-install` behind `hostashi-pg-client-install@<major>.service`, started by the agent through `phpctl` (ops `pg.client.install`, `pg.client.status`; API `GET`/`POST /api/v1/db-servers/postgresql-client`, scope `db_servers:write`). Allowlist: major 19, the newest on apt.postgresql.org for trixie and resolute on 2026-11-21. Key `B97B0AFCAA1A47F044F244A07FCC7D46ACCC4CF8` (no expiry date). The pin gives the repository priority 100 and 600 only to `postgresql-client-<major>`, `postgresql-client-common` and `libpq5`: the client 19 needs `libpq5 >= 19.0`, so the server's `libpq5` comes from apt.postgresql.org too. unattended-upgrades takes fixes from `origin=apt.postgresql.org,archive=<codename>-pgdg`.
- **`internal/pgctl` [V: CI job "PostgreSQL client" against servers 14, 17 and 19].** The tools run from `/usr/lib/postgresql/<major>/bin`, not `/usr/bin`: there `pg_wrapper` picks the version by its own rules. The connection goes through libpq's environment (`PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGSSLMODE`, `PGSSLROOTCERT`; `HOME`, `PGSERVICEFILE` and `PGSYSCONFDIR` point nowhere). The passfile holds one line `*:*:*:<user>:<password>` with `:` and `\` escaped. TLS: `verify` is `verify-full` with the system CA bundle, `verify-ca` the uploaded CA, `none` is `disable`. `Exec` (psql) puts `SET ROLE` to the site role first and runs the file in one transaction with `ON_ERROR_STOP`; `Restore` runs `pg_restore --clean --if-exists --no-owner` in script mode and feeds the SQL to psql the same way (one transaction, `SET ROLE`, `ON_ERROR_STOP`): pg_restore 17 and newer write `SET transaction_timeout = 0` into the preamble, which servers before 17 refuse, and its own `--single-transaction` stops on that line [V: pg_restore 19.0 against PostgreSQL 14]. Hostashi drops only that line from the preamble. The script starts with `\restrict`, so psql refuses meta-commands in it [V].
