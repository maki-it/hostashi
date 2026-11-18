# ADR-0024 MariaDB databases per site

**Status:** Proposed. **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0003 (stack), ADR-0004 (web/agent privilege split), ADR-0017 (CloudPanel import), ADR-0018 (API), [`03-features.md`](../03-features.md) 3.4 (row "MariaDB/MySQL: create/delete DB and user", P0), [`05-security.md`](../05-security.md) 5.6.

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Creating and deleting databases and database users is P0 for phase A [V: `03-features.md`]. Kim's WordPress sites on web2 need it for the migration (ADR-0017) [V: Kim, 2026-11-18].
- The rules say "typed names, no string SQL" and "identifier quoting and prepared statements where possible" [V: `05-security.md` 5.6].
- Debian 13 ships MariaDB 11.8 [V: `mariadb-server` 11.8.6 in a `debian:trixie` container, 2026-11-19]. `root@localhost` there is `IDENTIFIED VIA mysql_native_password USING 'invalid' OR unix_socket`, so only a process running as root can log in as root, and there is no root password [V: same container]. Ubuntu 26.04 is assumed to package the same way [A].
- `github.com/go-sql-driver/mysql` v1.10.2 (released 2026-11-17, current on 2026-11-19) logs in as root over the socket with that setup, although it has no client side for `unix_socket` [V: test in the container]. It is pure Go and adds one more module, `filippo.io/edwards25519` [V: `go.mod`].
- MariaDB accepts no `?` placeholder in `CREATE USER ... IDENTIFIED BY` and `ALTER USER` (error 1064) [V: test in the container]. The password must be a string literal.
- In `GRANT ... ON db.*`, `_` and `%` in the database name are LIKE wildcards. A grant on `` `a_b` `` also gave access to a database `axb` [V: test in the container]. Escaped as `` `a\_b` `` it does not [V].
- Not every site needs a database (static sites). Sites get none automatically [V: Kim, 2026-11-18].
- Kim decided that database and user names are free to choose, not prefixed with the site, so a migrated `wp-config.php` can keep `DB_NAME` and `DB_USER`, and that the agent keeps its own list (Kim, 2026-11-18).

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. The agent talks to MariaDB itself over the socket as root, with a Go driver** | No process start, no shell, no administrator password anywhere. Statements are built in one place from validated names [V: `internal/mariadb`] | One new dependency. Statements are strings, because MariaDB has no placeholders for these statements (see context) |
| B. The agent runs the `mariadb` client with `-e` | No dependency | A new `os/exec` exception that passes SQL text, which is exactly the general interface the rules forbid [V: `05-security.md` 5.6] |
| C. The panel holds an administrator account | Simple | The unprivileged web process would hold the keys to all databases, against ADR-0004 [A] |

## Recommendation

**Option A.**

- Agent operations `db.create`, `db.delete`, `db.list`, `db.user.create`, `db.user.delete`, `db.user.password`. No operation takes SQL.
- Names: databases `^[a-z][a-z0-9_]{0,63}$`, users `^[a-z][a-z0-9_]{0,31}$`. Server and distribution names (`mysql`, `sys`, `root`, `debian-sys-maint`, ...) and the prefixes `hostashi` and `mariadb` are refused.
- Passwords: 12 to 128 characters of printable ASCII without space, quotes, backslash and backtick. This makes the literal safe without escaping and independent of `sql_mode`. The panel generates one when none is given; a password can be given for a migration.
- Every user belongs to exactly one database, gets `ALL PRIVILEGES` on it and nothing else, and only `@'localhost'` (the socket, what PHP uses for `DB_HOST` `localhost`).
- The agent manages only what it created and recorded in `/var/lib/hostashi-agent/databases.json`. An existing database or user of the same name is a conflict, never adopted or dropped. This also protects the system databases.
- `db.delete` drops the users of the database first. `site.delete` is refused while the site owns a database.
- Errors never contain the statement or the server's message, only the step and the error number, because both can contain the password.
- The panel stores no database password. It shows a generated one once.

## Consequences

- Panel tables for databases and users, `/api/v1` endpoints and two scopes `databases:read` and `databases:write` (ADR-0018).
- The installer installs `mariadb-server` from the distribution. It keeps the package defaults (listen on `127.0.0.1` only, root via `unix_socket`) [A: Debian default `bind-address = 127.0.0.1`, to be checked in the installer test].
- A CI job runs the tests as root against the distribution's MariaDB in a Debian 13 container, including the wildcard case.
- Remote access, read-only users, import/export and SSO into a DB tool are later features and need their own changes.
- If the agent crashes between `CREATE DATABASE` and writing its list, the database exists on the server but is unmanaged. A new `db.create` of that name then answers "conflict". Cleanup is manual [A: rare].

## Open questions for Kim

1. Is "the panel stores no database password, it shows a generated one once" right, or should it be kept encrypted so it can be shown again?
