# ADR-0063 Resource usage per site

**Status:** Accepted (Kim, 2026-11-24). **Date drafted:** 2026-11-24. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0015 (resource limits), ADR-0022 (metrics), ADR-0032 (SSH per site), ADR-0034 (firewall), ADR-0035 (roles), ADR-0052 (server dashboard), ADR-0053 (site logs), ADR-0058 (OpenAPI and CLI).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants to see CPU, memory and network per site, like the server dashboard, to find out which site uses the most (2026-11-24).
- The dashboard (ADR-0052) shows host numbers only: the agent's read-only `host.stats` returns raw counters from `/proc`, the panel samples every 15 s and keeps a ring buffer of 15 s points for 1 hour (memory), 5 min averages for 48 hours and 1 h averages for 30 days (SQLite, `host_samples`) [V: `internal/servermon`, `internal/store/servermon.go`].
- Every site has its own slice `hostashi.slice/hostashi-site.slice/hostashi-site-<site>.slice`. The site's PHP-FPM pool, cron jobs, object cache, Node.js, app and Python units, the file manager unit and deploy runs run in it [V: `internal/siteslice`, `Slice=` in `internal/php`, `internal/cron`, `internal/objcache`, `internal/appunit`, `internal/filesunit`, `internal/deployunit`]. SSH and SFTP sessions of the site user run in `user-<uid>.slice`, which gets the site's limits through a drop-in [V: `internal/siteslice`].
- The slice units set `MemoryAccounting=yes` and `TasksAccounting=yes`, not `IOAccounting=` [V: `internal/siteslice/siteslice.go`].
- cgroup v2 gives per cgroup: `cpu.stat` (`usage_usec`, always present), `memory.current` and `memory.stat` (with `inactive_file`), `memory.peak` (kernel 5.19+), `pids.current`, and `io.stat` (bytes and operations per device, only when the io controller is on for the parent) [U: kernel cgroup-v2 docs; Debian 13 and Ubuntu 26.04 kernels are newer than 5.19 [U]].
- `memory.current` includes the page cache of files the site read. A site that serves many files looks big although the kernel can drop that cache at any time [U]. Container tools show the "working set", `memory.current` minus `inactive_file` [U: cAdvisor, Docker].
- The agent already reads per-site cgroups for `metrics.snapshot` (PHP and object cache memory, disk use of the home every 15 minutes) [V: `internal/sitestats`, `internal/agent/metrics.go`].
- **Network has no cgroup counter.** cgroup v2 counts no bytes per cgroup [U]. In addition, visitors' traffic does not run through the site's processes: nginx (outside the slice) terminates TLS and serves static files; PHP only answers nginx over a Unix socket [V: `internal/web/nginx.go`]. So a counter on the slice would see only what the site's processes send themselves (API calls, mail, downloads, SSH/SFTP), not what the site serves.
- nginx writes one access log per site, `/var/log/nginx/sites/<site>/access.log`, in the `combined` format or `hostashi_realip`; the format has `$body_bytes_sent` but not `$bytes_sent` or `$request_length`, and the log can be switched off [V: `internal/web/nginx.go`]. Stock nginx from the distribution has no per-vhost traffic counters without a third-party module [U].
- nftables can count packets per cgroup in the output hook with `socket cgroupv2 level <n> "<path>"` [U: nft man page]. The host firewall's output chain is `policy accept` [V: `packaging/firewall/hostashi.nft`].

## Options

**1. Where CPU, memory, tasks and disk I/O come from**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. The site's slice and its `user-<uid>.slice`, read by the agent from cgroup files** | Exact, includes every process of the site, cheap (a few small files per site), no new `os/exec` | `io.stat` needs `IOAccounting=yes` on the slices |
| B. Per process from `/proc/<pid>` | Works without cgroups | Misses short-lived processes (cron, builds), costs more, needs a mapping from process to site |

**2. Network per site**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Web traffic from a small extra nginx log per site; outgoing traffic of the site's processes later through nftables counters** | Web traffic is what a site serves and what Kim compares; nginx counts exact bytes; the outgoing counter is a good signal for abuse (spam, a hacked site) | Two numbers with different sources; the second needs firewall changes |
| B. Both at once | Complete from the start | More work in one go, touches the firewall before the rest is proven |
| C. Count from the existing access log | No extra log | The log can be off, has no request size, its format belongs to the user (ADR-0053, ADR-0059) |
| D. No network per site | Least work | Kim asked for it |

**3. Where the samples are kept**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. As for the host (ADR-0052): the panel samples every 15 s, ring buffer in memory, 5 min and 1 h tiers in SQLite** | One design, rates computed in one place, history survives a restart | One migration, rows grow with the number of sites |
| B. Only live values, no history | No storage | No "which site used the most yesterday" |
| C. Only through `/metrics` and an external Prometheus | Nothing to store | Kim does not run Prometheus; ADR-0052 says the panel must work without one |

## Recommendation

**1A, 2A, 3A.**

**What is measured per site**

- **CPU:** `usage_usec` of the site's slice plus its `user-<uid>.slice`, as a rate. Shown as percent of one core (100 % = one full core, as `top`) and as share of the whole server.
- **Memory:** working set (`memory.current` minus `inactive_file` from `memory.stat`) of both slices; the page cache is shown separately as "cache" in the details. The site's memory limit (ADR-0015) is drawn as a line when one is set.
- **Tasks:** `pids.current`, with the site's `TasksMax`.
- **Disk I/O:** read and write bytes per second from `io.stat`, summed over devices. The installer and the slice units set `IOAccounting=yes` [A: no measurable overhead on current kernels [U]].
- **Web traffic:** bytes in (`$request_length`), bytes out (`$bytes_sent`) and requests per second from a second, compact log per vhost `access_log /var/log/nginx/sites/<site>/traffic.log hostashi_traffic buffer=32k flush=5s;` with the fixed format `$msec $status $request_length $bytes_sent`. It is written even when the user switched the normal access log off, and it holds no IP address, no path and no user agent, so it is no personal data [A]. The agent reads only what was appended since its last read (offset and inode per site, a rotated file is finished from `traffic.log.1`), sums the numbers and never returns lines. logrotate keeps one day [E].
- **Outgoing traffic (second step, after the rest):** one nftables counter per site in the output chain (`socket cgroupv2 level 3 "hostashi.slice/hostashi-site.slice/hostashi-site-<site>.slice" counter`), plus the user slice for SSH/SFTP. Rendered from typed values by `internal/firewall` like the panel file; read with `nft -j list counters` through the firewall's existing exception [A: check the exact exception in the PR]. Its own short addendum to this ADR before it is built.
- **Disk use** stays as it is: the agent's 15-minute walk of the home (ADR-0015, ADR-0022).

**Agent and panel**

- New read-only agent op `sites.stats`: for every site the raw counters above (CPU microseconds, memory, cache, tasks, I/O bytes, web bytes and requests since start), sorted by name, at most `MaxSites`. Sites without a slice yet (no PHP, nothing running) answer zeros. Paths come from the site name only, never from a request, with the `sitelogs` rules for opening logs (no symlinks, expected owner).
- `internal/servermon` polls it with `host.stats` every 15 s and keeps per-site tiers like the host's: 15 s points for 1 hour in memory, 5 min averages for 48 hours and 1 h averages for 30 days in a new table `site_samples` (site, tier, time, the numbers). 50 sites take about 65,000 rows [E: well under 10 MB]. Rows go with the site (`ON DELETE CASCADE`).
- `/metrics` (ADR-0022) gets counters `hostashi_site_cpu_seconds_total`, `hostashi_site_memory_bytes`, `hostashi_site_io_bytes_total{direction}`, `hostashi_site_http_bytes_total{direction}`, `hostashi_site_http_requests_total`, label `site` only.

**UI**

- **Overview, "Sites by usage" (admins):** a table of all sites with CPU, memory, web traffic and disk I/O for a chosen range (1 h, 24 h, 7 d, 30 d), sortable, with a small trend line per row; the top sites are also a card on the server dashboard.
- **Site, tab "Usage":** the same charts as the server dashboard for this site (CPU, memory with limit, web traffic, disk I/O, tasks), range 1 h / 24 h / 7 d / 30 d as on the dashboard.
- Screenshots to Kim before the UI PR.

**Roles, API and CLI (ADR-0018, ADR-0035, ADR-0058)**

- `GET /api/v1/sites/{name}/usage?range=1h|24h|7d|30d` (the ranges of `GET /api/v1/dashboard`) (`sites:read`, everyone who can see the site): the points of the site.
- `GET /api/v1/usage/sites?range=…&sort=cpu|memory|traffic|io` (`services:read`, admins only, as the dashboard): the ranking. A site user never sees other sites' numbers.
- Both with `mux.Op` and an OpenAPI entry; CLI `hostashi api usage sites` and `hostashi api sites usage NAME`.

**Not in v1:** alerts on site usage (could reuse ADR-0055 later), traffic per URL or per visitor, monthly traffic quotas, database CPU per site (MariaDB runs in one process for all sites [V: ADR-0057]).

## Consequences

- No new `os/exec` exception for steps 1–5; the agent only reads files. The outgoing counter (step 6) is checked against the firewall's exception in its addendum.
- nginx config: one extra `access_log` line and `log_format` per vhost; logrotate rule for `traffic.log`. A site with heavy traffic writes about 30 bytes per request more [E].
- Database: MariaDB and PostgreSQL work for all sites is not in any site's slice, so a site whose load is mostly SQL looks smaller than it is. The UI says so next to the CPU chart [A].
- nginx's own CPU for TLS and static files is not counted per site either; the web traffic numbers stand in for it.
- Store: one migration for `site_samples`. Number from the coordinator when the PR starts.
- Tests: cgroup parser tests with fixture trees (slice missing, memory accounting off, `io.stat` empty), the traffic log reader (partial last line, rotation, truncation, a symlink instead of the file), tier aggregation per site, API tests for roles (site user only own site, ranking admin-only), the installer test checks that a request to a site shows up in its traffic counter and that a busy PHP loop raises its CPU, on both distributions.
- Rough order of PRs: (1) agent op `sites.stats` for CPU, memory, tasks, I/O with `IOAccounting=yes`, (2) traffic log and reader, (3) servermon tiers, migration, API, `/metrics`, (4) UI after screenshots, (5) wiki, (6) outgoing counter after its addendum [E: 6 PRs].

## Open questions for Kim

None left.

1. ~~Network: web traffic from a small nginx log now and outgoing traffic of the site's processes later (2A), or both at once (2B), or none (2D)?~~ Decided (Kim, 2026-11-24): web traffic now, outgoing traffic later (2A).
