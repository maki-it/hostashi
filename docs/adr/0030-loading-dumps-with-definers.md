# ADR-0030 Loading imported dumps with definers

**Status:** Proposed. **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0024 (database passwords), ADR-0025 (database import and export), ADR-0026 (CloudPanel migration transfer, Part 2).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- The CloudPanel import dumps each database of a site into `<home>/cloudpanel-import-db/` [V: ADR-0026 Part 2]. Loading a dump is the hardened import of ADR-0025: a temporary user with `ALL` on the target database, dropped afterwards, the client as the site user with `--sandbox` [V].
- Views, triggers, stored routines and events in a dump name a `DEFINER` of the old server, for example `` DEFINER=`shop`@`localhost` ``. MariaDB lets a user create an object for another definer only with `SET USER` or `SUPER` [U]. Such dumps fail today [V: installer container test dumps `olddb2` with a view and a trigger and does not load it].
- Even when the definer is stripped, an object created by the temporary user names that user, which is dropped after the load; a trigger then fails at the first insert [U].
- How many of the 22 databases on web2 have such objects is not known [U]; WordPress core creates none [A].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. An owner account per database; the agent strips `DEFINER` clauses while it streams the dump** | Objects get the owner as definer and keep working after the load. The owner has `ALL` on that one database only, a random password nobody keeps, and is locked (`ACCOUNT LOCK`) after each load, so nobody can log in as it; a lock stops logins, not objects that name the account as definer [U: to be shown by a container test]. No extra privilege for anyone | One more reserved account per loaded database (`hostashi_own_<16 hex>`), dropped with the database. A small filter in Go that only removes the `DEFINER=...` part of `CREATE` statements and of the `/*!50013 ... */` and `/*!50017 ... */` comments that `mariadb-dump` writes |
| B. Strip `DEFINER` and turn views and routines into `SQL SECURITY INVOKER` | No extra account | Triggers always run as their definer; with the temporary user gone they fail [U] |
| C. Give the temporary user `SET USER` | No rewrite | It could create objects that run as root; breaks layer 1 of ADR-0025 |
| D. Leave it: such databases are loaded by hand as root | Nothing to build | A manual step per database, as root, with a dump from another server |

## Recommendation

**Option A.**

- The owner account `hostashi_own_<16 hex>@localhost` is created on the first load into a database, gets `ALL PRIVILEGES` on that database only, is unlocked for the load and locked again right after, also when the load fails. Its password is random and never stored [A].
- The agent reads the dump and writes it to the client's standard input through the filter; the file itself is not changed. Every other layer of ADR-0025 stays: the client runs as the site user, `--sandbox`, options from a pipe, files only below the home [A].
- The filter removes `DEFINER=<user>@<host>` with backquoted or quoted names, nothing else. Statements without a definer pass unchanged. It is fuzz-tested [A].
- Loads of CloudPanel dumps use the owner account; other imports (ADR-0025) keep the temporary user. The admin may choose the owner load for any import later [A].
- Deleting the database drops its owner account; `hostashi_own_` is reserved like `hostashi_job_` (ADR-0024) [A].
- After the import job is done, the panel starts one load per dump into its target database, so the admin does not start them by hand [A].

## Consequences

- No new `os/exec` exception; `internal/mariadbctl` keeps its fixed arguments [A].
- A container test loads `olddb2` with its view and trigger, checks that the view answers and the trigger fires after the load, and that nobody can log in as the owner [A].

## Open questions

1. Is a locked owner account per loaded database acceptable (option A)?
