# SQLite under load

Date: 2026-11-20. Question: is SQLite enough for the panel database of a single server with realistic load, up to about a thousand sites? Short answer: **yes, with a wide margin.** The check found two weak spots, both fixed in the same change: writers could wait more than a second under contention, and the site list ran one query per site.

Labels as in the rest of the docs: **verified** (measured or read in the code), **assumption**, **estimate**.

## What the panel asks of the database

Verified from the code (`internal/store`, `internal/serve`, `internal/hosting`, `internal/auth`):

- Only `hostashi serve` opens the database. The agent keeps its own state files, so there is one process and no cross-process contention in normal operation.
- Every panel request looks up the session (`SessionUser`, one indexed read). Every API request looks up the token and records its use at most once a minute (`TouchAPIToken`).
- Every change writes its row and an audit entry. Multi-row changes run in a transaction that starts with `BEGIN IMMEDIATE` (`_txlock=immediate`), so a transaction never has to upgrade a read lock to a write lock.
- `Sites()` (all sites with their domains) backs the site list, every Prometheus scrape (`CertExpiries`), the hourly TLS worker and a few other lists.
- The hourly backup copy runs `VACUUM INTO` while the panel keeps working.
- Cron jobs, backups and PHP run outside the panel (systemd units). They do not write to the panel database while they run.

Assumption: realistic peak load is one to three admins in the browser, some API automation and a metrics scrape every 15 seconds. That is well under 50 requests per second, and well under 10 writes per second.

## Settings before this change

Verified in `internal/store/store.go`:

| Setting | Value | Assessment |
|---|---|---|
| `journal_mode` | WAL | Right: readers never block the writer and the writer never blocks readers. |
| `busy_timeout` | 5000 ms | Right, but with many writers it was the only queue (see below). |
| `synchronous` | NORMAL | Right for WAL: a commit survives a crash of the process; a power loss can lose the last commits, never corrupt the file. |
| `foreign_keys` | ON | Right. |
| `_txlock` | immediate | Right: no deadlock between two transactions that both want to write. |
| Pool | one `*sql.DB`, no limits | Weak: database/sql keeps only two idle connections, so bursts kept opening new connections and running the pragmas again; and every connection could try to write. |

## How it was measured

`internal/store/load_test.go`, not part of the normal test run:

- Benchmarks: `go test ./internal/store -run '^$' -bench . -benchtime 2s`
- Mixed load: `HOSTASHI_SQLITE_LOAD=10s HOSTASHI_SQLITE_LOAD_SITES=1000 go test ./internal/store -run TestSQLiteLoad -v`

The fixture has n active sites with three domains, two cron jobs and one database each, one admin with 20 sessions and an API token, and 10 000 audit entries. The mixed load runs at the same time, without any pause:

- 32 readers, each in a loop: session lookup, site by name, cron jobs of a site.
- 8 writers, each in a loop: toggle a site flag in a transaction, append an audit entry, record a token use.
- The site list once a second and `VACUUM INTO` every two seconds.

That is a saturation test, far above the assumed real load. It shows the upper limit and the tail latency under contention.

Machine (verified): cloud container, 4 vCPU Intel Xeon 2.1 GHz, 15 GB RAM, ext4 on a virtual disk. Assumption: a current Hetzner server with NVMe is as fast or faster, so the numbers are a lower bound.

## Results

### Single operations (benchmarks, no contention)

| Operation | Before | After |
|---|---|---|
| Session lookup (parallel) | 8.5 µs | unchanged |
| Site by name (parallel, 300 sites) | 42 µs | unchanged |
| Audit entry (parallel writers) | 71 µs | unchanged |
| Site flag in a transaction (parallel) | 43 µs | unchanged |
| All sites, 22 sites | 0.46 ms | 0.20 ms |
| All sites, 300 sites | 5.6 ms | 2.1 ms |
| All sites, 1000 sites | 20.6 ms | 6.7 ms |
| `VACUUM INTO`, 1000 sites | 14 ms | unchanged |

### Mixed load, 10 seconds, no errors in any run

Writes (each of the three write operations had about the same numbers):

| Sites | Before p50 / p99 / max | After p50 / p99 / max |
|---|---|---|
| 22 | 0.5 ms / 87 ms / 1.04 s | 3.8 ms / 28 ms / 57 ms |
| 300 | 0.7 ms / 114 ms / 1.69 s | 4.1 ms / 27 ms / 55 ms |
| 1000 | 0.7 ms / 116 ms / 1.05 s | 4.2 ms / 32 ms / 69 ms |

Reads and periodic jobs at 1000 sites:

| Operation | Before p50 / p99 | After p50 / p99 |
|---|---|---|
| Session lookup | 44 µs / 7.1 ms | 45 µs / 3.7 ms |
| Site by name | 1.1 ms / 16 ms | 0.9 ms / 8.0 ms |
| All sites | 433 ms / 552 ms | 30 ms / 59 ms |
| `VACUUM INTO` | 172 ms / 222 ms | 173 ms / 185 ms |

Throughput after the change, 1000 sites: about 41 000 reads per second (14 000 per read operation) and about 1 300 writes per second together, on 4 vCPU. Before: about 30 000 reads and 1 200 writes per second.

After the run at 1000 sites the database file was 2.2 MB and the WAL file 11 MB. The WAL stays large while readers never pause (a checkpoint cannot reset it), and is reused from the start once the load drops.

## What changed

1. **One write connection.** Writes go through their own pool with a single connection. Writers now wait in Go, in order, and a canceled request leaves the queue at once. Before, up to 40 connections competed in SQLite's busy handler, which polls with growing sleeps, so an unlucky writer waited more than a second while later ones overtook it. The worst write went from 1.7 s to under 70 ms. The median rose from under 1 ms to about 4 ms, because writers now queue instead of retrying in a tight loop; at the assumed real load there is no queue and the median stays at the single-operation time. The 5 s `busy_timeout` stays as the limit against other processes.
2. **Read pool is `query_only`.** The read pool sets `PRAGMA query_only`, so a write sent there by mistake fails in the tests instead of competing for the lock. `VACUUM INTO` only reads the live database, but `query_only` refuses it, so the copy lifts the flag on its own connection and sets it again before the connection goes back to the pool (a connection where that fails is discarded).
3. **More idle read connections.** The read pool keeps 16 idle connections instead of the default two, so bursts do not open new connections and rerun the pragmas.
4. **Site list in two queries.** `Sites()` reads all domains in one query instead of one query per site: 20.6 ms to 6.7 ms at 1000 sites idle, 433 ms to 30 ms under the mixed load.

Tests in `internal/store/pool_test.go` cover the read pool refusing writes, the flag being restored after a copy, 32 parallel writers without errors, a canceled write leaving the queue while reads go on, and the site list keeping each site's domains in order.

## Assessment

- Verified: at 1000 sites and roughly a hundred times the assumed real load, every operation succeeded and no write waited longer than 70 ms.
- Estimate: the assumed peak (under 50 requests and under 10 writes per second) uses less than 1 % of the measured capacity. SQLite is not the limit of a single-server panel; the agent operations behind a change (nginx reload, useradd, systemd) take far longer than the database write.
- Estimate: memory. SQLite's default page cache is 2 MB per connection; with 16 idle read connections and a database of a few MB, the cache stays under about 40 MB.

## Not covered, and when to look again

- **Audit log growth.** The audit log is append-only and never pruned. Estimate: at 1 000 entries a day it grows by roughly 0.1 MB a day; lists of the log need paging before it reaches millions of rows. Not urgent for v1.
- **Job queue.** The architecture (04, "Jobs") plans a persistent job queue in SQLite. It does not exist yet. When it comes, its writes go through the same write connection and should keep transactions short (no agent call or network request while a transaction is open).
- **Power loss.** `synchronous=NORMAL` can lose the last commits on a power loss. That is accepted (the reconciler compares desired and live state on start), but was not tested here.
- **Multi-server.** Out of scope for v1 (ADR-0006 already notes PostgreSQL for that case).
- Look again when the job queue lands, or if a server ever has more than a few thousand sites.
