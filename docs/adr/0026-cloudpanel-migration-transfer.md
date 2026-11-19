# ADR-0026 Data transfer for the CloudPanel migration

**Status:** Accepted (2026-11-19, by Kim); Part 4 Proposed. **Date drafted:** 2026-11-19. **Decides:** Kim. **Related:** ADR-0017 (CloudPanel import as migration), ADR-0025 (database import and export), ADR-0008 (backups with restic), ADR-0004 (web/agent privilege split), [`05-security.md`](../05-security.md), spike S6 ([`REPORT.md`](../../spike/s6-adopt/REPORT.md)).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- ADR-0017 decides that the import is a migration: the old CloudPanel server is scanned read-only, the new Hostashi server gets the files and databases, and the old server is shut down afterwards [V: ADR-0017].
- Kim chose on 2026-11-19 that **the new server pulls the data over SSH**, the scan runs on the old server as in S6, and the old server is only read [V: Kim, thread]. Rejected: an export archive on the old server (needs free space on a 38 GB disk), and copying by hand.
- The scan already exists as `hostashi cloudpanel scan` and writes a JSON report with a format version [V: PR 92].
- Reading every site needs root on the old server: homes, `/etc/nginx`, crontabs and certificate keys are not readable by one site user [V: S6, the scan ran as root].
- A root SSH key on the new server that can run any command on the old server would make the new server a master key for the old one. The transfer must not need more than reading files and reading databases.
- The agent may start processes only through reviewed `os/exec` exceptions [V: `.golangci.yml`]. `golang.org/x/crypto/ssh` is already a dependency [V: `go.mod`, used for the backup host key].
- An agent call has a 10 second limit [V: ADR-0025]. Copying a site takes minutes to hours [E], so the transfer runs as a job, like a restore (`hostashi-restore@<id>.service`) [V: `packaging/systemd`].
- Sites must be copied twice: a first full copy while the old site is still live, and a short final sync just before the DNS switch, so the downtime per site stays short [A].

## Part 1: how files are read

| Option | Pro | Contra |
|--------|-----|--------|
| **A. SFTP in Go (`github.com/pkg/sftp` over `x/crypto/ssh`), old side locked to a read-only SFTP server** | No new process in Hostashi. The old side can enforce read-only in `authorized_keys`. Every write on the new side is our own code with our own checks | New dependency. No rolling-checksum delta: a changed file is copied whole |
| B. `rsync` over `ssh`, old side locked with `rrsync -ro` | Boring tech, fast deltas, resumable | New `os/exec` exception for `rsync` and `ssh`. rsync on the receiving side runs as root and writes into a site-owned tree, which needs care against symlink races [A] |
| C. Read-only `sshfs` mount, then the S6 scanner and a local copy | One code path for scan and copy | FUSE on the new server, a mount as root, a third process [A] |

**Recommendation: A.** The old server is protected by its own configuration, not by our good behaviour: the key that Kim adds on the old server only allows a read-only SFTP session and one port forward (Part 2):

```text
restrict,port-forwarding,permitopen="127.0.0.1:3306",permitlisten="127.0.0.1:1",command="/usr/lib/openssh/sftp-server -R" ssh-ed25519 AAAA... hostashi-import
```

`restrict` turns off a shell, agent and X11 forwarding and a terminal; `command=` replaces whatever the client asks to run; `sftp-server -R` refuses every write; `permitopen` limits port forwarding to the database [V: `TestLockedKeyAgainstOpenSSH` against the distribution's sshd on Debian 13 and Ubuntu 26.04, 2026-11-19]. `port-forwarding` also allows remote forwarding, a listener on the old server: without `permitlisten` the test could open one. `authorized_keys` has no `permitlisten="none"` (OpenSSH then ignores the whole key [V: same test, OpenSSH 9.6]), so the line permits only `127.0.0.1:1`, a port nothing on the old server connects to, and every other listener is refused [V]. `permitopen` matches the text of the request, so only `127.0.0.1:3306` opens, not `localhost:3306` [V]. The path of `sftp-server` differs between distributions [U], so the panel shows the line for the path it detects in the scan report (or the person fixes it).

The final sync compares size and modification time per file and copies only what changed. Whole-file copies are enough for the expected data [E: web2 has 38 GB in total, most of it in files that do not change between the two runs].

## Part 2: how databases are read

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Port forward to MariaDB on the old server, local `mariadb-dump` through the tunnel, a read-only dump user on the old server** | Uses the existing `internal/mariadbctl` exception and the hardened import from ADR-0025. No command runs on the old server | Kim creates one database user on the old server by hand (one SQL statement, shown by the panel) |
| B. Run `mariadb-dump` on the old server over SSH | No database user | The key would have to allow a command, so it could no longer be locked to read-only SFTP; a dump command line is a shell string on the old side |
| C. Use CloudPanel's own database root password | No manual step | Reads a secret from CloudPanel's files and uses full root rights on the old database, more than needed [A] |

**Recommendation: A.** The dump user `hostashi_import` on the old server gets only `SELECT, SHOW VIEW, TRIGGER, EVENT, LOCK TABLES` and connects from `127.0.0.1`, because a forwarded connection arrives over TCP from sshd [V: the installer container test dumps through the locked key and sshd with exactly this account]. `TRIGGER` and `EVENT` are needed to dump triggers and events but also allow creating them, so the user is dropped on the old server when the migration is finished (`DROP USER 'hostashi_import'@'127.0.0.1'`) [A].

The agent generates the password (32 letters and digits) and keeps it root-only (`0600`) next to the SSH key in its state directory; the panel shows it once, inside the SQL statement to run on the old server, and never again. It is removed with the migration source and when the old server's address changes [V: `cpsource` tests, container test]. This differs from the first draft, where the panel stored it encrypted with the panel key: the job that uses it runs as root next to the SSH key, which already reads more than the database, so a second copy in the panel would only add a place to leak it [A].

The job dumps before it drops to the site user: it listens on a Unix socket in a new directory of its private `/tmp` that only the site user can enter, forwards each connection over the SSH session to `127.0.0.1:3306`, and runs `mariadb-dump` as the site user through `internal/mariadbctl` with the same fixed arguments as an export [V: `cpimport` tests, container test]. Each database lands in its own new file `<home>/cloudpanel-import-db/<new name>-<UTC time>.sql`, created like an export's (`0600`, the site user's, no symlinks) [V]; nothing is written on the old server. Loading a dump is the hardened import of ADR-0025 (temporary user, `--sandbox`, client as the site user) [V: container test]. Views, triggers and routines carry a `DEFINER` of the old server's users, which that import refuses [V: container test dumps them; A: ADR-0025]; WordPress databases normally have none [A], and rewriting definers comes with the step that imports automatically.

## Part 3: who does the transfer on the new server

- The private key is generated by Hostashi (ed25519), lives root-owned with mode `0600` in the agent's state directory, and is never shown. The panel shows the public key and the `authorized_keys` line [A: same pattern as the backup key].
- The old server's host key is pinned: the person pastes it once (as for the backup storage box), and every connection checks it. No trust on first use [V: the backup flow does this today].
- One systemd job per site and run, `hostashi-import@<id>.service`, started by the agent, like a restore. The job reads the key as root, opens the SSH connection, runs the database step, and then **drops to the site's user and group** (`setresgid`, `setresuid`, no supplementary groups) before it writes a single file of the old tree; only the dump files are created as root and handed to the site user, as for an export. So a symlink or a hostile file name from the old tree can at worst damage that one site, which the old tree already controls [V: `cpimport.Drop` checks all three uids and gids and that the way back fails; `TestDrop` and the installer container test, where the copied files belong to the site user].
- Files land in `<home>/cloudpanel-import`; every further run, including the final sync, works on that directory and never on the live web root [V: `hostashi-import@.service` and the installer container test]. Activating the import (agent op `cloudpanel.import.activate`, `POST /api/v1/migrations/cloudpanel/sites/{name}/activate`) exchanges it with `htdocs` in one `renameat2(RENAME_EXCHANGE)`, so the site always has a web root, sets the web root's mode and keeps the previous one as `htdocs-before-import-<UTC time>`. It is refused while an import runs or when the directory is not the site user's or a symlink [V: `cpimport` tests, container test]. A run after activating copies into a new `cloudpanel-import` from scratch [A].
- The writer refuses device files, FIFOs and sockets, never sets setuid or setgid bits, rewrites ownership to the site user, keeps symlinks as symlinks without following them, and opens every path with `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS)` below the target directory [A: same rules as S4].
- Paths are taken from the scan report only (home and document root), validated again on the new server, and must lie below `/home/<user>/` on the old side [V: the scanner blocks anything else].

## Part 4: loading the dumps (Proposed, 2026-11-19)

Views, triggers, stored routines and events in a dump name a `DEFINER` of the old server, for example `` DEFINER=`shop`@`localhost` ``. The hardened import of ADR-0025 runs as a temporary user that is dropped afterwards, and MariaDB lets a user create an object for another definer only with `SET USER` (or `SUPER`) [U]. Such dumps fail today [V: container test]. How many of the 22 databases on web2 have such objects is not known [U]; WordPress core creates none [A].

| Option | Pro | Contra |
|--------|-----|--------|
| **A. An owner account per database; the agent strips `DEFINER` clauses while it streams the dump** | Objects get the owner as definer and keep working after the load. The owner has `ALL` on that one database only, a random password nobody keeps, and is locked (`ACCOUNT LOCK`) after each load, so nobody can log in as it; a lock stops logins, not objects that name the account as definer [U: to be shown by the container test]. No extra privilege for anyone | One more reserved account per imported database (`hostashi_own_<16 hex>`), dropped with the database. A dump parser in Go that only rewrites the `DEFINER=` part of `CREATE` statements and `/*!50013 ... */` / `/*!50017 ... */` comments |
| B. Strip `DEFINER` and turn views and routines into `SQL SECURITY INVOKER` | No extra account | Triggers always run as their definer; with the temporary user gone they fail at the first insert [U] |
| C. Give the temporary user `SET USER` | No rewrite | It could create objects that run as root; breaks layer 1 of ADR-0025 |
| D. Leave it: such databases are loaded by hand as root | Nothing to build | A manual step per database, as root, with a dump from another server |

**Recommendation: A.** Loading runs after the import job: the panel starts the existing `db.import` job for each dump with a flag `owner: true`, so ADR-0025's other layers (client as the site user, `--sandbox`, options from a pipe, files below the home) stay as they are [A]. A container test loads `olddb2` with its view and trigger and checks that the trigger fires after the load [A].

## Consequences

- Kim's manual steps on the old server: add one `authorized_keys` line for root, create one read-only database user, copy the scan report to the new server, and remove the line and the user afterwards. Nothing else changes on the old server [A].
- New dependency `github.com/pkg/sftp` v1.13.12, the latest release on 2026-11-19 (BSD-2-Clause [V: its LICENSE]), with `github.com/kr/fs` (BSD-3-Clause [V]).
- No new `os/exec` exception. `internal/mariadbctl` dumps from a local socket of the tunnel, with the same fixed arguments [V: `cpimport.dumpDatabases`].
- The new server holds a key that can read every file of the old server, including other sites' secrets and certificate keys, until the line is removed. The panel shows a reminder to remove it after the migration, and the key is deleted when the migration is closed [A].
- Not covered here: the DNS switch per site, cron, TLS certificates (Let's Encrypt certificates are issued again on the new server [V: S6 finding 6]), custom snippets (ADR-0017 Part 2). They come in their own steps.

## Open questions

1. ~~Is a root `authorized_keys` line on the old server acceptable when it is locked to read-only SFTP and one port forward?~~ Decided by Kim (2026-11-19): yes [V: Kim, thread]. A separate user with read rights on all homes would need more changes on the old server and still read the same data [A].
