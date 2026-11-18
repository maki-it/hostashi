# ADR-0025 Database import and export

**Status:** Proposed. **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0024 (MariaDB per site), ADR-0017 (CloudPanel import), ADR-0004 (web/agent privilege split), [`03-features.md`](../03-features.md) 3.4 (row "Import/export (gzip)", P0), [`05-security.md`](../05-security.md) 5.6.

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Import and export of databases (gzip) is P0 [V: `03-features.md`]. The migration of the WordPress sites from web2 needs import [V: Kim, 2026-11-18].
- A dump is SQL text written by someone else. Imported as root, it could change any database or create accounts [A: standard SQL].
- The `mariadb` client runs `system` and `\!` lines from its input as shell commands, with the rights of the client process. As root this ran `id` as root [V: Debian 13 container, MariaDB 11.8.6, 2026-11-19]. With `--sandbox`, `system`, `\!`, `source` and `tee` are refused [V: same container].
- An import run as a temporary user that only has privileges on the target database cannot reach another database (`USE other` gives error 1044) and cannot create users (error 1227) [V: same container]. Without the FILE privilege, `LOAD_FILE` returns NULL and `INTO OUTFILE` fails [V].
- A MySQL 8 dump with `COLLATE=utf8mb4_0900_ai_ci` imports into MariaDB 11.8 [V: same container]. Whether web2 runs MySQL or MariaDB is not known [A].
- The agent may not start processes except through reviewed `os/exec` exceptions (`internal/sysuser`, `internal/webctl`, `internal/phpctl`) [V: `.golangci.yml`].
- An agent call has a 10 second limit, and the panel's HTTP server a 30 second write timeout [V: `internal/agentrpc/server.go`, `internal/serve/serve.go`]. A dump of a large site takes longer [E: minutes for hundreds of MB].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. A fourth `os/exec` exception `internal/mariadbctl` for `/usr/bin/mariadb` and `/usr/bin/mariadb-dump`** | The distribution's own tools, which know every dump format and edge case. Boring tech | A new process start in the agent; the client parses untrusted input |
| B. Dump and import in Go over the driver | No new process | Our own SQL parser for dumps (delimiters, triggers, routines, binary data), which is exactly the kind of code that breaks on real dumps [A] |
| C. The site user runs the client over SSH | Nothing in Hostashi | No panel or API function, and every user must know the database password |

## Recommendation

**Option A**, hardened in layers, so that each one alone already stops the attack from the context:

1. **Temporary user per run.** The agent creates `hostashi_job_<random>@localhost` with a random password and `ALL PRIVILEGES` on the target database only, and drops it afterwards. The dump never runs with root's rights in the database.
2. **The client runs as the site's Linux user**, never as root, with a minimal environment. Even a shell escape would only have the site's own rights.
3. **`--sandbox`** for `mariadb`, and the options only from a pipe (`--defaults-file=/dev/fd/3`), never from argv, so the password does not show up in the process list.
4. **Files only inside the site home.** The agent opens the dump file (import) or creates it (export, `O_EXCL`) with `RESOLVE_BENEATH` below the home and refuses symlinks, like the file manager rules in `05-security.md` 5.6. Paths in a request are relative to the home. `.sql` and `.sql.gz` are accepted, gzip is done in Go.
5. **Fixed argv.** Only the database name is a variable; it is validated by ADR-0024's rule.

Long runs become **agent jobs**: `db.import` and `db.export` start a job and return its ID, `db.job` reports state and a short, fixed error text. One job per database at a time. The CLI waits for the job. The API endpoints follow when the panel has its job queue (`04-architecture.md` 4.7); until then import and export go through the CLI as root on the server.

## Consequences

- `.golangci.yml` gets a fourth exception for `internal/mariadbctl`.
- An import into a database that already has tables can fail half way; there is no automatic rollback. The docs say: import into an empty database [A: dumps usually start with `DROP TABLE IF EXISTS`].
- `DEFINER` clauses for other users and statements that need SUPER fail under the temporary user [A]. WordPress dumps normally have neither [A].
- A temporary user left behind after an agent crash is removed at the next agent start (it matches the `hostashi_job_` prefix, which ADR-0024 reserves) [A].

## Answers from Kim

1. The fourth `os/exec` exception for `mariadb` and `mariadb-dump` is acceptable, with the hardening above (option A) [V: Kim, 2026-11-19].

## Verified

- In a Debian 13 container with MariaDB 11.8, dumps with `system`, `\!`, `USE mysql`, `CREATE USER` and `LOAD_FILE` all fail under the temporary user and `--sandbox`; a MySQL 8 WordPress dump (`utf8mb4_0900_ai_ci`) imports [V: `internal/mariadb/server_dumps_test.go`].
- The client opens `/dev/fd/3` again by path, so the pipe must belong to the site user; the agent changes its owner before the start [V].
