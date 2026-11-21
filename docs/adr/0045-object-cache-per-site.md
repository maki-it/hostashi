# ADR-0045 Object cache (Valkey) per site

**Status:** Accepted (Kim, 2026-11-21). **Date drafted:** 2026-11-21. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0015 (per-site limits and slices), ADR-0017 (import and presets), ADR-0019 (PHP versions), ADR-0026 (CloudPanel transfer), ADR-0033 (hardening baseline), ADR-0035 (roles), ADR-0036 (PHP versions from the panel), ADR-0037 (UI redesign).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Sites on web2, the CloudPanel server Hostashi replaces, use Redis today, so v1 needs an object cache per site [V: Kim, 2026-11-20].
- `03-features.md` lists "Redis (per site, auth, Unix socket)" as P1 [V]. CloudPanel ships Redis [V: `01-cloudpanel-research.md`]. How it is set up there (one shared instance on `127.0.0.1:6379`, password or not) is not known; the S6 scan does not look at Redis [V: `spike/s6-adopt/REPORT.md`, "not discovered"]. Assumption: one shared instance without isolation between sites [A].
- Every site has its own Linux user, its own PHP-FPM unit `hostashi-php-<version>@<site>` and its own slice `hostashi-site-<site>.slice` with `MemoryMax`, `TasksMax` and so on (ADR-0015) [V: `packaging/systemd/hostashi-php@.service`, `internal/phpctl`].
- The agent already starts and stops per-site units through `systemctl`, the existing `os/exec` exception in `internal/phpctl`, with unit names checked against fixed patterns [V: `internal/phpctl/phpctl.go`, `unitRe`, `sliceRe`].
- `redis` is already a reserved site user name [V: `internal/agentapi/sites.go`].
- Packages, checked on 2026-11-21 [V: sources.debian.org, packages.ubuntu.com]:

  | Package | Debian 13 (trixie) | Ubuntu 26.04 (resolute) |
  |---------|--------------------|-------------------------|
  | `valkey-server` | 8.1.1 (main, security update `deb13u2`, `deb13u3` in proposed-updates) | 9.0.4 (universe) |
  | `redis-server` | 8.0.2 (main) | 8.0.5 (universe) |
  | `php-redis` (phpredis client) | 6.2.0 (main) | 6.2.0 (universe) |

- Licenses [V: the packages' `debian/copyright`]: Valkey is BSD-3-Clause. Redis 8 is offered under RSALv2, SSPLv1 or AGPLv3, at the user's choice. phpredis is PHP-3.01.
- The Debian package of Valkey ships a templated unit `valkey-server@.service` that already runs one instance per name on a Unix socket, with a strong systemd sandbox (`ProtectSystem=strict`, `PrivateUsers`, `MemoryDenyWriteExecute`, `NoExecPaths=/`, empty capability set) [V: `debian/bin/generate-systemd-service-files`, 8.1.1+dfsg1-3+deb13u2].
- Valkey forked from Redis 7.2.4 and speaks the same protocol; clients such as phpredis, Predis and the common WordPress, Laravel and Magento integrations work with it unchanged [U].

## Options

### Engine

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Valkey from the distribution** | BSD-3 license, no questions next to AGPL code or for customers; in both distributions; Debian keeps it in main with security updates (ADR-0033 unattended-upgrades) | On Ubuntu only in universe: security fixes depend on the community or Ubuntu Pro [U]. Different version per distribution (8.1 and 9.0) |
| B. Redis 8 from the distribution | The original name that apps document | Tri-license with source-available options; AGPLv3 is fine for Hostashi itself, but it is a question for every operator we would rather not raise [U]; also universe on Ubuntu |
| C. Memcached | Simple, in main on both [U] | Not what the sites on web2 use; WordPress plugins and Magento expect Redis semantics [A] |
| D. Upstream Valkey packages or our own build | Same version everywhere | Hostashi would own security updates; against boring tech |

### Isolation

| Option | Pro | Contra |
|--------|-----|--------|
| **A. One instance per site, running as the site user in the site's slice, Unix socket only** | A site cannot read or flush another site's keys; memory counts against the site's own `MemoryMax`; a crash or flood affects one site; no TCP port, no password to store | One process per site with a cache (a few MB each when empty [E]) |
| B. One shared instance, one database number or ACL user per site | One process | Shared memory: one site evicts another's keys; ACL passwords have to be stored and handed to sites; a bug in ACL or one leaked password crosses sites [A] |
| C. One instance per site as a shared `valkey` user | Close to the Debian template | Sockets need group tricks so that only one site user reaches them; memory not in the site slice |

## Recommendation

**Engine A, isolation A**, as an opt-in per site.

- **Package.** The installer installs `valkey-server` and `php-redis` for each installed PHP version (distro `php<version>-redis`, or the sury.org package where ADR-0036 installs the version) and disables the package's default `valkey-server.service`, so nothing listens on port 6379. The package's own templated unit stays unused.
- **Unit.** A Hostashi template `hostashi-valkey@<site>.service`, written by us (AGPL) and modelled on the hardening of the Debian template and of `hostashi-php@.service`:
  - `User=` and `Group=` are the site user, set by the agent's drop-in like the PHP units; `Slice=hostashi-site-<site>.slice`, so the site's memory and task limits include the cache.
  - `ExecStart=/usr/bin/valkey-server /var/lib/hostashi-agent/valkey/<site>.conf --supervised systemd --daemonize no`.
  - Sandbox: `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp`, `PrivateDevices`, `PrivateNetwork=yes` (no TCP at all), `RestrictAddressFamilies=AF_UNIX`, `CapabilityBoundingSet=` empty, `NoNewPrivileges`, `MemoryDenyWriteExecute`, `NoExecPaths=/` with `ExecPaths=/usr/bin/valkey-server /usr/lib /lib`, `LimitCORE=0` (ADR-0033), `Restart=on-failure`.
  - `RuntimeDirectory=hostashi-valkey/<site>`, mode `0700`, so the socket `/run/hostashi-valkey/<site>/valkey.sock` (mode `0600`) is reachable only by the site user, which is what its PHP workers, cron jobs and SSH sessions run as.
- **Config.** The agent renders the config from a fixed Go template; the only variables are the site name (already validated) and the memory size:
  - `port 0`, `unixsocket` and `unixsocketperm 600`, no `bind`.
  - Cache only: `save ""`, `appendonly no`, no data directory. A restart empties the cache.
  - `maxmemory <size>` with `maxmemory-policy allkeys-lru`. Default 64 MB, choices 32 to 1024 MB [E]. The size must stay below the site's `MemoryMax`; the panel checks it.
  - `enable-protected-configs no`, `enable-debug-command no`, `enable-module-command no` [U: Valkey defaults, to verify in tests], and the default user loses `@admin` (for example `CONFIG`, `DEBUG`, `MODULE`, `REPLICAOF`, `SHUTDOWN`, `MONITOR`) while keeping `FLUSHDB`, `FLUSHALL`, `INFO` and `KEYS`, which cache plugins use [U: exact ACL categories to verify]. This closes the classic "`CONFIG SET dir` then write a file" attack even though the sandbox already limits it to nothing writable.
  - No password: the socket permissions are the access control, and nothing secret has to be stored or shown. A password can be added later if an app insists on one [A].
- **Agent operations** (typed, in `internal/agentapi`):
  - `cache.enable(site, memory_mb)`: writes the config, the slice drop-in, and starts the unit.
  - `cache.set_memory(site, memory_mb)`: rewrites the config and restarts the unit (empties the cache).
  - `cache.flush(site)`: restarts the unit. With no persistence that empties it; no Valkey client in the agent.
  - `cache.disable(site)`: stops the unit and removes config and drop-in.
  - `cache.status(site)`: unit state and the unit's `MemoryCurrent` through the existing `systemctl` call.
  - *Implemented as* (2026-11-21): `cache.apply(site, memoryMb)` covers enable, set_memory and disable (0 turns the cache off), like `cron.apply` replaces a site's state in one call; `cache.flush` and `cache.status` as above. A disabled site's cache is stopped and comes back with the site.
  - **No new `os/exec`.** The only process call is `systemctl` in `internal/phpctl`; its unit pattern gets one more fixed form, `^hostashi-valkey@<site>\.service$`. The agent never runs `valkey-cli`.
- **Panel.** A section "Object cache" on the site page (ADR-0037): switch on or off, size, "Flush cache", the state and the connection details to copy: socket path and the lines for `wp-config.php` (`WP_REDIS_SCHEME` `unix`, `WP_REDIS_PATH`) [U: constant names of the Redis Object Cache plugin]. Hostashi does not install or edit WordPress plugins. Roles (ADR-0035): admin and manager on all sites, including the size; a `user` on granted sites may turn it on or off and flush it, with the size set by an admin or manager or the preset (decided, Kim, 2026-11-21).
- **Presets.** Presets (ADR-0017, ADR-0015) get a field "object cache: off or size", default off.
- **Doctor and metrics.** doctor checks that `valkey-server.service` is disabled, that every enabled cache unit runs, and that no socket is world-reachable. Metrics (ADR-0022) export cache memory per site from the unit.
- **Backups.** Nothing to back up: the cache holds no data that is not elsewhere.

### CloudPanel import

- The scanner (ADR-0026) learns two things on web2, both as root and without reading site files: whether `redis-server` runs and how it listens (from its config), and, through a plain `INFO keyspace` over the local connection from Go (no `redis-cli`), which database numbers hold keys. It cannot tell which site uses which database [A].
- The import plan therefore asks per site "object cache: on or off", pre-selected on when the site's preset has it on, and shows a warning when web2 runs Redis: after the move, apps still pointing at `127.0.0.1:6379` find nothing there and have to switch to the socket shown in the panel.
- Cache contents are not migrated. A cache starts empty; that is safe by definition [A: holds only if no app on web2 keeps sessions or queues in Redis, see open questions].

## Consequences

- One more process per site that uses the cache; its memory is part of the site's limit, so heavy cache use can make PHP hit `MemoryMax` sooner. The panel says so next to the size.
- Two more packages on every server (`valkey-server`, `php-redis` per PHP version), even when no site uses the cache, so that switching it on needs no apt run from the panel.
- Different Valkey versions on Debian 13 (8.1) and Ubuntu 26.04 (9.0); the features we use exist in both [U].
- The container tests on both distributions start a cache unit, connect as the site user through PHP, check that another site user cannot connect, and that `CONFIG SET` is refused.

## Risks to name openly

- **Ubuntu universe.** On Ubuntu 26.04, `valkey-server` and `php-redis` are in universe; security fixes are not guaranteed by Canonical without Ubuntu Pro [U]. Debian 13 has both in main.
- **Same user as the site.** The cache runs as the site user. A site that is already compromised can read and change its own cache, which it could anyway; it cannot reach other sites' caches through the socket, because the directory is `0700` per site user.
- **Cache poisoning inside a site.** Apps that unserialize cache data trust it. This is the app's risk, not changed by Hostashi, but worth knowing.
- **Not a pentest.**

## Open questions

1. ~~Which apps on web2 use Redis, and for what?~~ **Answered (Kim, 2026-11-21): as far as Kim knows, only as an object cache through a WordPress plugin; no sessions or queues.** So the cache-only design holds; the import warning stays as a check [A: not verified on web2].
2. ~~Valkey from the distribution, one instance per site as the site user, over a Unix socket only?~~ **Decided (Kim, 2026-11-21): yes.**
3. ~~May the role `user` switch the cache on and off for granted sites, or only flush it?~~ **Decided (Kim, 2026-11-21): `user` may switch it on and off and flush it on granted sites, within the site's limits; only admin and manager change the size.**
