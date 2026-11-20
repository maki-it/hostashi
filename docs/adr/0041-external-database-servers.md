# ADR-0041 External database servers

**Status:** Proposed. **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0008 (backups), ADR-0024 (MariaDB per site, no stored passwords), ADR-0025 (import and export), ADR-0033 (hardening), ADR-0035 (roles), ADR-0037 (UI redesign), ADR-0038 (web database tool).

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

1. MariaDB and MySQL 8.0+, or MariaDB only?
2. Panel process talks to external servers, agent stays without network (option A)?
