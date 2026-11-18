# Spike S2: reconciler with generations

**Status:** throwaway prototype, not product code. Nothing here is a decision; any follow-up goes through an ADR (ADR-0003 is Accepted, a failed S2 would have reopened it).

**Question (from [docs/06-roadmap.md](../../docs/06-roadmap.md) S2 and [docs/04-architecture.md](../../docs/04-architecture.md) 4.3):** Are generation switch, `nginx -t`, reload and rollback robust? Abort criterion: with failure injection, a broken vhost must never cause downtime.

Evidence tags as in [docs/adr/README.md](../../docs/adr/README.md): **[V]** verified by test or code in this spike, **[A]** assumption, **[E]** estimate, **[U]** unverified general knowledge.

**Result: the abort criterion holds.** In every injected failure the previous generation kept serving, and a probe that hit the good site every millisecond during each case saw 0 failed requests [V]. The pattern needs one addition that the architecture doc does not mention yet (finding 1).

## Environment and limits of this spike

- Sandbox container, root, Ubuntu 24.04, kernel 6.18, Go 1.24.7, **real nginx 1.24.0 from the Ubuntu archive** [V]. The Debian 13 and nginx.org packages were not tested, behavior of `nginx -t` and reload is expected to be the same [A].
- Sites listen on a high loopback port instead of 80 and 443. No TLS, no PHP-FPM pools, no systemd units, no nftables, no cron. Only the nginx part of the reconciler was built, behind a `Driver` interface that mirrors `WebServerDriver` (Render, Validate, Activate, Verify).
- The worker user is `root` in the tests so that temp directories are readable. Nothing about the switch logic depends on it.
- Nothing was created at Hetzner, no server was touched.

## What was built

Module `spike/s2-reconciler`, standard library plus the S1 validators (local `replace`). About 940 lines of Go without tests, 460 lines of tests.

| File | Content |
|------|---------|
| `reconcile/state.go` | Typed desired state (`Site`, `Desired`) with positive validation: user and domain from S1, absolute clean root, port range, snippet size. Error values. |
| `reconcile/driver.go` | The `Driver` interface. |
| `reconcile/nginx.go` | `NginxDriver`: renders `nginx.conf`, one server file and one snippet file per site; `nginx -t`; start or reload; verification. All commands as argv, with a timeout. |
| `reconcile/reconciler.go` | `Apply`, `Plan` (diff preview), `Drift`, `Recover`; generations, manifest, atomic symlink switch, rollback, pruning, lock. |
| `reconcile/diff.go` | Small line diff for the preview. |

### Layout and flow

```text
<base>/generations/<n>/        committed, never modified: nginx.conf, sites/, snippets/,
                               generation.conf, desired.json, MANIFEST (sha256 per file), CONFIRMED
<base>/generations/<n>.tmp/    being written, removed on recovery
<base>/generations/<n>.failed/ rejected or rolled back, kept for debugging (FAILED holds the reason)
<base>/current -> generations/<n>    swapped with symlink + rename
```

`Apply`: validate desired state, take a flock, render, stop if the content hash equals the confirmed current generation and no drift exists, write `<n>.tmp` with fsync, rename to `<n>`, run `nginx -t` on `<n>`, switch the symlink, reload, verify, write `CONFIRMED`. Any failure after the switch re-points `current` at the previous generation, reloads and verifies that one.

Design choices that made this simple:

- `nginx.conf` uses only **relative includes**. nginx resolves them against the directory of the `-c` file, so the same bytes are tested at `generations/<n>/` and served through `current/` [V: validation of the final path passes, the served config is the validated one].
- The generation number lives in a separate meta file (`generation.conf`, a `return 200` on a loopback-only control server). It is excluded from the content hash, so identical desired state gives identical hashes and `Apply` is a no-op [V].
- Reload is done with `-s reload` and `-c <base>/current/nginx.conf`. nginx re-reads the config path it was started with, and the symlink is resolved again on each reload [V].

## Results

| Check | Result |
|-------|--------|
| First apply starts nginx, serves the site, generation 1 live | [V] `TestFirstApplyServesAndIsIdempotent` |
| Same desired state again: no new generation, no reload | [V] same test (Activate counter unchanged) |
| Add and remove sites while a probe hits another site | [V] `TestChangeAddsAndRemovesSites`: 305 requests, 0 failed |
| Invalid desired state (shell characters in domain, `..` and spaces in root, bad user, bad port, unknown kind, NUL in snippet, duplicates) | [V] `TestInvalidDesiredStateWritesNothing`: rejected, no file written |
| Snippet with unknown directive, unknown variable, unterminated block | [V] rejected by `nginx -t`, nothing activated, 0 failed probe requests |
| Snippet that closes the server block (`}` then a new `server {`) | [V] rejected: nginx parses every included file on its own, an unmatched `}` is an error |
| Snippet that includes a missing file | [V] rejected by `nginx -t` |
| Valid config but the site returns 500 | [V] caught by the health check, rolled back, 0 failed probe requests |
| Valid config but dead upstream (502) | [V] caught by the health check, rolled back |
| Valid config but a `listen` that is already taken | [V] `nginx -t` **passes**, reload "succeeds", nginx keeps the old config. Caught only because the generation number never changes. Rolled back after the 3 s timeout, 2077 probe requests, 0 failed |
| Activate fails, and the rollback fails too | [V] `ErrRollbackFailed` is returned, old nginx still serves, `Recover` heals it |
| Crash (process death) after tmp write, after commit, after switch, after reload | [V] `TestCrashRecovery`: a fresh `Recover` leaves generation 1 live, no `.tmp` left, next apply works and never reuses a number |
| `Recover` starts a stopped nginx from the confirmed generation | [V] `TestRecoverStartsStoppedServer` |
| Drift: hand-edited, deleted or extra files in a generation | [V] `Drift` names each one, `Plan` shows the edit, `Apply` of the same state writes a clean new generation (the "overwrite" choice) |
| Diff preview does not write anything | [V] `TestPlanShowsDiffWithoutWriting` |
| Two runs at once | [V] second one gets `ErrBusy` (flock) |
| Pruning keeps current plus N confirmed generations, bounds failed ones | [V] `TestPruneKeepsRecentConfirmedGenerations` |

`go test -race -count=3` passes (31 s). `go vet` passes for amd64 and arm64, tests were not run on arm64.

## Findings

1. **`nginx -t` plus an asynchronous `nginx -s reload` is not enough.** `-t` does not bind sockets, and a reload that nginx rejects at runtime (bind conflict, for example) only writes to the error log while old workers keep serving. The signal command returns success either way. The fix that worked: every generation exposes its number on a loopback control endpoint, and the reconciler treats "the new number is live" as part of the apply. A generation that never becomes live is rolled back [V]. **Add to 4.3:** the health check must include "the new generation is the one answering".
2. **One matching answer is not proof either.** After a reload, old workers still accept connections for a short time. The first version of `Verify` accepted a single answer with the new number and then checked the sites against a worker that still ran the old config: a broken new site was reported healthy, and later a rollback was reported done while the new config still answered. The fix is eight consecutive matching answers (10 ms apart) before the site checks [V, both races reproduced and fixed by this]. The number is a heuristic, not a proof [A]. A stronger design (a per-generation header on every response) is blocked because a snippet with its own `add_header` hides inherited headers [U].
3. **Confining the custom snippet to its own file is what keeps typed rendering safe.** All typed fields are positively validated, so injection can only come through the snippet. nginx rejects an unmatched `}` per file, so a snippet cannot break out of its server block [V]. A snippet can still do anything a server block allows (proxy, `return`, extra `listen`, `root`). That is a feature gap in the "unmanaged snippet" idea and not a safety property: an authorized panel user can already serve arbitrary content on its own domain, but a snippet could also `listen` on another site's port or `server_name` [A, not tested]. Needs a decision when custom snippets are designed.
4. **Content hash plus manifest gives cheap idempotency and drift detection.** No extra state: the manifest in the generation directory is the record [V].
5. **Crash safety comes from three rules:** write to `<n>.tmp` then rename, switch with symlink plus rename, and treat only generations with a `CONFIRMED` file as safe to return to. After a crash `current` may point at an unconfirmed generation, and `Recover` moves it back [V]. Numbers are never reused because failed and tmp names count.
6. **Cost is small.** An apply with a reload took about 100 to 200 ms in the sandbox [V, from test timings], the bind-conflict rollback waits for the 3 s timeout. Generation size is a few KB per site, so keeping a handful is free [E].

## Not covered

- PHP-FPM, systemd units, cron and nftables as further reconciler targets. `php-fpm -t` and `nft -c` are expected to fit the same `Validate` step, but FPM pools are started per pool and an invalid pool may need a different rollback [A].
- TLS certificates (reload after renewal, missing certificate file, OCSP), HTTP/3 and real ports 80 and 443. Listen-socket handover on reload is nginx's job [U].
- Large configs: 22 sites were not simulated. Time of `nginx -t` and reload with hundreds of sites is unknown [E: linear].
- Disk full during write, read-only filesystem, and power loss (fsync was used but not tested with fault injection).
- The `unmanaged` mode and an editable snippet UI. Only the include structure exists.
- A real agent process: the reconciler is a library here and not wired to the S1 RPC. Locking is per directory, not per host.
- Debian 13 nginx and the nginx.org package.

## How to run

```sh
apt-get install -y nginx        # only the binary is used, the system service is never started
cd spike/s2-reconciler
go test ./...                   # skips without nginx on PATH
go test -race -count=3 ./...
```

Tests create temporary directories under `/tmp` (`s2spike*`), start their own nginx on random loopback ports with a private pid file and stop it afterwards. They do not touch `/etc/nginx` or the system service.
