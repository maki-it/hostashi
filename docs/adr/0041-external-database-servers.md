# ADR-0041 External database servers

**Status:** Accepted (Kim, 2026-11-20); Addendum 1 Proposed (2026-11-21). **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0008 (backups), ADR-0024 (MariaDB per site, no stored passwords), ADR-0025 (import and export), ADR-0033 (hardening), ADR-0035 (roles), ADR-0037 (UI redesign), ADR-0038 (web database tool).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants to add external database servers next to `localhost` under "Databases", pick one when creating a database, see each server's version in the list, and mark one server as the default that is preselected [V: Kim, thread, 2026-11-20].
- Kim accepts that Hostashi stores the administrator password of an external server, encrypted with the server key [V: Kim, thread, 2026-11-20]. This is a deliberate exception to ADR-0024, which stores no database passwords; ADR-0024 stays valid for the passwords of site users.
- The agent talks to the local MariaDB as root over the Unix socket and has no network: its unit sets `IPAddressDeny=any` [V: `packaging/systemd/hostashi-agent.service`].
- The panel process (`hostashi`, unprivileged) has network access and already encrypts secrets with `/etc/hostashi/secret.key` through `internal/secrets` (AES-GCM with a purpose string) [V: `internal/secrets/secrets.go`, `internal/serve/serve.go`].
- Database dumps for backups run in `hostashi-backup.service`, which has network access [V: `packaging/systemd/hostashi-backup.service`, `internal/resticctl`].

## Options

### Who talks to the external server

| Option | Pro | Contra |
|--------|-----|--------|
| **A. The panel process, with typed operations like the agent's database code** | Has network already; the agent keeps `IPAddressDeny=any`; no root needed for a remote server | The unprivileged process holds an administrator credential of the external server while it runs [A] |
| B. The agent | One place for all database code | The root agent would need network access, which ADR-0033 removed on purpose |

### Which servers

| Option | Pro | Contra |
|--------|-----|--------|
| **A. MariaDB and MySQL 8.0+ over TCP, TLS required** | Covers managed databases and a separate DB host [A] | Two dialects to test; MySQL differs in user and grant syntax details [U] |
| B. MariaDB only | One dialect | Many hosted offers are MySQL [U] |

## Recommendation

**A and A.**

- **Server list.** Table `db_servers` in the panel store: name, host, port, admin user, sealed password (`secrets.Seal`, purpose `db-server:<id>`), TLS mode (`verify` by default; `verify-ca` with an uploaded CA for private CAs; `none` for a trusted private network, decided by Kim on 2026-11-20), `is_default`. `localhost` is a fixed row that cannot be deleted; it is the default until another server is marked.
- **Unencrypted connections.** Allowed on purpose for servers in a private network (vSwitch, cloud network). The dialog warns that passwords and data cross the network in plain text; the server list marks such a server, and doctor reports it as a warning, not an error.
- **Version.** The list shows engine and version (`SELECT VERSION()`, for example "MariaDB 11.8.6"), read when the server is added and refreshed every 15 minutes and on demand, plus state (reachable, TLS ok, last error).
- **Default.** Exactly one server is the default; the "New database" dialog preselects it. Changing the default does not move existing databases.
- **Adding a server.** The dialog tests the connection, TLS and the needed privileges (`CREATE`, `DROP`, `CREATE USER`, `GRANT OPTION` on the databases it will manage) before saving. Hostashi recommends a dedicated admin user, not `root`.
- **Databases.** `databases.server_id` points at the server. Create, delete, users and new passwords work as for `localhost` (ADR-0024): typed operations, strict names, `_` escaped in grants, a user per database. Database users on an external server are created for the address the server sees from this host (`'user'@'<this server's IP>'`), not `%`. Hostashi manages only databases it created, as today.
- **Backups.** The backup unit dumps external databases over TLS with the stored credential, which it opens with the server key [A: the unit runs as root and can read the key]. An unreachable server marks the backup "partial" and shows under "Needs attention"; the local part is still saved.
- **Import and export (ADR-0025)** and **Adminer (ADR-0038)** work for external databases: the temporary user is created on that server with the same limits, Adminer's pool gets network access only to the configured server addresses [A].
- **Deleting a server** is only possible when no database uses it.
- **Roles.** Managing servers is admin-only (new scope `db_servers:write`); choosing a server when creating a database is allowed for everyone who may create databases.
- **Firewall.** Outgoing connections are not limited by the host firewall today (ADR-0034); nothing changes.

## Consequences

- New store table and migration, API under `/api/v1/db-servers`, a server field on `POST /api/v1/databases`.
- The "Databases" page gets a second table "Servers" (ADR-0037).
- Tests against MariaDB and MySQL containers in CI, with TLS.
- doctor reports unreachable servers and certificates that expire soon.

## Risks to name openly

- **Stored administrator credential.** A compromise of the panel process or of `/etc/hostashi/secret.key` exposes the external server's admin access. Mitigation: a dedicated admin user with rights only for Hostashi's databases, TLS with verification, audit log of every use [A].
- **Unencrypted connections** expose the admin password and data to anyone on the path; only for private networks.
- **Network failures.** Site databases on another host make sites depend on that host and the network between.
- **MySQL differences.** Grant and user syntax differ in details between MariaDB and MySQL [U]; the tests must cover both.

## Open questions

0. ~~TLS required?~~ **Decided (Kim, 2026-11-20): no, unencrypted connections are allowed with a warning.**

1. ~~MariaDB and MySQL 8.0+, or MariaDB only?~~ **Decided (Kim, 2026-11-20): both.**
2. ~~Panel process talks to external servers, agent stays without network (option A)?~~ **Decided (Kim, 2026-11-20): yes.**

## Addendum 1: import, export and Adminer for external databases

**Status:** Proposed (2026-11-21). **Decides:** Kim.

### Context

- The first implementation (PRs #244, #248, #264, #266) refuses import, export and Adminer for databases on external servers (`db_server_local_only`) [V: `internal/hosting/dbdumps.go`, `internal/hosting/dbtool.go`]. The line "Import and export … work for external databases" in the recommendation above is therefore not done yet.
- Both jobs run in places without network: the import and export run in the agent (`IPAddressDeny=any`), Adminer runs in `hostashi-adminer.service` with `RestrictAddressFamilies=AF_UNIX` and `IPAddressDeny=any` [V: `packaging/systemd/hostashi-agent.service`, `packaging/adminer/hostashi-adminer.service`].
- Kim chose a separate unit with network access only to the database server over moving the feature after v1 [V: Kim, decision card in the thread, 2026-11-21].
- There is a precedent: `hostashi-import@<id>.service` (ADR-0026) is started by the agent, has network, runs `mariadb-dump` as the site user and writes into the site's home [V: `packaging/systemd/hostashi-import@.service`, `internal/resticctl/systemd.go`].

### Proposal

**Import and export: `hostashi-dbjob@<id>.service`**

1. The panel creates a temporary database user on the external server with privileges on the one database only, as the agent does locally (ADR-0025), with a random password and a name like `hostashi_job_<8 hex>`. The panel keeps the administrator credential; the job never sees it.
2. The panel calls a new typed agent operation `db.job.start_remote` with: kind (import or export), database, site, relative path, server host, port, TLS mode, CA (PEM), the resolved IP addresses of the server, the temporary user and its password. The agent validates every field with the existing validators.
3. The agent writes a job file (root, 0600) into its state directory and a drop-in for this instance only: `IPAddressDeny=any`, `IPAddressAllow=` the resolved addresses, and `ReadWritePaths=` the site's home, as for `hostashi-import@`. Then it starts the unit with the existing `systemctl start --no-block` exception (fixed template name, id of 16 hex digits). No new `os/exec` exception: the unit uses `internal/mariadbctl` (already allowed to run `mariadb` and `mariadb-dump`).
4. The unit reads and removes the job file, opens the dump file in the home as today, and runs `mariadb` (with `--sandbox`) or `mariadb-dump` as the site user. The credentials go through the pipe (`--defaults-file=/dev/fd/3`) as today, now with `host`, `port` and the TLS options (`ssl-verify-server-cert`, `ssl-ca` from a 0600 file in a private temp directory, or `skip-ssl` for `none`). `--host` is the resolved IP address and `ssl-verify-server-cert` checks the name the admin entered [A: needs a test; the MariaDB client may check the certificate against `--host`, then the unit maps the name to the address in a private `/etc/hosts` instead].
5. When the job ends, the agent reports the state like a local job; the panel drops the temporary user. A temporary user that outlives its job (panel restart) is removed by the 15-minute server check [A].

Sandbox of the unit: like `hostashi-import@`, plus `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6`, no DNS needed (addresses are resolved by the panel), capabilities only `CAP_SETUID CAP_SETGID CAP_CHOWN CAP_DAC_OVERRIDE`, all dropped after the switch to the site user.

**Adminer**

- The panel creates the temporary user on the external server (same limits and lifetime as for local tickets) and puts host, port, TLS mode and the CA path into the ticket.
- The wrapper `index.php` connects to the ticket's server instead of the fixed socket; it accepts only servers that are in the ticket, so a user cannot point Adminer elsewhere.
- `hostashi-adminer.service` gets `AF_INET`/`AF_INET6` and an `IPAddressAllow=` drop-in with the addresses of all configured external servers. The agent rewrites the drop-in and restarts the pool when the server list changes (new typed operation `dbtool.servers`).
- Risk: unlike the job unit, the Adminer pool is long-running and can reach every configured external server, not only the one of the current ticket. A compromised Adminer process could try other servers, but it holds no credential for them [A].

### Consequences

- New agent operations `db.job.start_remote` and `dbtool.servers`; new unit `hostashi-dbjob@.service`; installer and container test learn the unit.
- The CI `dbserver` job gets an import and export test against MariaDB and MySQL with TLS.
- The `db_server_local_only` refusal is removed for import, export and Adminer.

### Risks to name openly

- **A second place with network next to the panel**: the job unit runs as root until it drops to the site user. Its network is limited to the server's addresses for one job.
- **Temporary users on the external server** that are not removed when the panel dies at the wrong moment; the periodic check removes them.
- **DNS changes**: the unit allows the addresses resolved when the job starts; a server that moves during a job fails that job.

### Open questions

1. Is Adminer for external databases part of v1 as described, or only import and export first? Recommendation: both, in that order (import and export first).
2. Resolve the server name in the panel (as proposed) or allow DNS in the unit? Recommendation: in the panel, so the unit needs no resolver.
