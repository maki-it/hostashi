# Spike S3: nginx vs. Caddy as web server driver

**Status:** throwaway prototype, not product code. Nothing here is a decision; ADR-0005 stays Proposed and Kim decides.

**Question (from [docs/06-roadmap.md](../../docs/06-roadmap.md) S3 and [ADR-0005](../../docs/07-decisions.md)):** Which driver fits better: rewrites, HTTP/3, FastCGI cache, templates? The roadmap asks for a decision with measurements.

Evidence tags as in [docs/adr/README.md](../../docs/adr/README.md): **[V]** verified by test or run in this spike, **[A]** assumption, **[E]** estimate, **[U]** unverified general knowledge.

## Recommendation

**Keep nginx (nginx.org package) as the first driver, as ADR-0005 proposes.** Two reasons carry it: the native FastCGI cache (WordPress is the main workload, and Caddy needs a third-party plugin and a custom build for it) and a clearly smaller footprint at 22 sites. Caddy is the better *operator experience* in two points (built-in ACME, synchronous reload errors), but S2 already solved the second one for nginx, and ADR-0003 already plans lego for ACME. The `Driver` interface fits both, so Caddy stays a possible second driver [A: only rendering was compared, no Caddy driver was built].

## Environment and limits

- Sandbox container, Ubuntu 24.04, 4 shared vCPUs, Go 1.26.11. nginx **1.30.6 from nginx.org** (stable, has `http_v3_module`), Caddy **v2.11.9** built with `go install` (not the official release binary), PHP 8.3.6 FPM (8 static children), `ab` as load tool [V].
- The same 22 generated sites for both: 12 PHP (WordPress-style front controller), 5 static, 5 reverse proxy to a small Go backend. Self-signed wildcard certificate for `*.test`, ports 18080/18443 on loopback. Both generated from one Go program (`gen/`) [V].
- **Numbers are indicative.** Each figure is one run on a shared machine, the load tool competes for the same CPUs, nothing was repeated. They show orders of magnitude, not exact ratios [V for the run, A for generalizing].
- Not tested: real ACME against Let's Encrypt (no outbound ACME here), real ports 80/443, real PHP applications, HTTP/3 under load, Debian 13 packages, the Caddy JSON API (the Caddyfile adapter was used).
- Nothing was created at Hetzner. Nothing was installed system-wide except `php8.3-fpm`, `apache2-utils` and the Ubuntu `nginx` package (never started as a service).

## Functional comparison

All scenarios ran against both servers with the same checks (`h3check` for HTTP/1.1, 2 and 3, `curl`, `ab`).

| Scenario | nginx 1.30.6 | Caddy 2.11.9 |
|----------|--------------|--------------|
| HTTP to HTTPS and `www` to apex redirect | [V] works | [V] works |
| WordPress front controller (`/blog/hello?p=7` reaches `index.php`, `$_GET` intact) | [V] `try_files $uri $uri/ /index.php?$args` | [V] `php_fastcgi` does it implicitly |
| Static files, long cache headers, deny `.env` and `wp-config.php` | [V] | [V] |
| Reverse proxy with WebSocket upgrade (101 through the proxy) | [V] needs the `map` for `Connection` and three `proxy_set_header` lines | [V] `reverse_proxy` alone |
| HTTP/3 | [V] works (`listen ... quic`, `http2 on`). Needs nginx.org or nginx at least 1.25. The Ubuntu 24.04 package (1.24) has no HTTP/3 | [V] works, on by default |
| `Alt-Svc` header | [V] manual `add_header`, and **lost in every location that has its own `add_header`** (static assets lost it in this test) | [V] automatic |
| Compression | [V] gzip configured, no zstd/brotli in the stock package | [V] `encode gzip zstd` (zstd output not verified on a test file) |
| FastCGI cache | [V] native, `X-Cache: MISS` then `HIT` | **Not in stock Caddy.** [V] works with the Caddy-org module `cache-handler` (Souin) after `xcaddy build`, `Cache-Status: Souin; hit` |
| Automatic HTTPS | not built in, needs lego or certbot (ADR-0003 plans lego) | [V] 22 names issued by Caddy's local CA, first full answer after 4.4 s. A real CA was not tested |
| Per-site custom snippets | `include` of an own file, parsed per file (S2 [V]) | `import` or own file, same idea [A] |
| Regex limits | PCRE: lookahead works | **Go RE2: no lookahead.** `^/\.(?!well-known)` fails, needs `not path /.well-known/*` instead [V, hit it while writing the generator] |

## Reload and failure behavior (links to S2)

| Case | nginx | Caddy |
|------|-------|-------|
| Unknown directive | [V] `nginx -t` fails | [V] `caddy validate` and `caddy reload` fail, old config stays |
| **Port already in use** | [V] `nginx -t` passes, `nginx -s reload` exits 0, error only in the error log, old config keeps running (matches S2 finding 1) | [V] `caddy validate` passes, but **`caddy reload` fails with `bind: address already in use` and exit 1**, old config keeps running |
| Dead upstream (502) | accepted, needs a health check (S2) | [V] accepted, needs a health check |
| Requests during reload (one probe request about every 15 ms, 4 to 6 s per case) | [V] 338 ok, 0 failed | [V] 385 ok, 0 failed |
| Reload time with 22 sites | [V] about 35 ms | [V] about 100 ms |

Caddy reports activation errors to the caller. With nginx the S2 pattern (generation number on a control endpoint) is needed. Both are reliable once the reconciler verifies the live state, so this is a convenience difference and not a safety one.

## Measurements (22 sites, ab, 50 concurrent, TLS, keep-alive, 20,000 requests)

| | nginx | Caddy |
|---|-------|-------|
| Idle memory (RSS, all processes) | 35 MiB (5 processes) | 56 MiB (1 process) |
| Memory after load | 51 MiB | 73 MiB |
| Static file, requests per second | 94,500 | 30,800 |
| PHP page via FPM, requests per second | 27,900 | 8,900 |
| Reverse proxy, requests per second | 28,100 | 13,100 |
| PHP with FastCGI cache hit | 77,500 | 30,700 (custom build with cache-handler, RSS 113 MiB) |

All runs: 0 failed requests, no non-2xx. All values are far above what one panel server with 22 sites needs, so the speed difference is **not a decision criterion** by itself; the memory difference of about 20 MiB is small too. The cache and the packaging matter more [A].

Template size for the same 22 sites: generated `nginx.conf` 727 lines (template 76 lines), generated `Caddyfile` 334 lines (template 49 lines) [V]. Caddy is shorter, but nginx's length does not cost anything once it is generated.

### A measurement trap worth keeping

The first PHP run showed nginx at 870 requests per second against Caddy at 8,800. Cause: PHP answers without `Content-Length`, nginx therefore sends `Transfer-Encoding: chunked`, and `ab` stalls on chunked responses over TLS. Caddy buffers small responses and sets a length. With an explicit `Content-Length` from the PHP script both servers behave normally (nginx 27,900). This was found by bisecting nginx configs (workers, quic, gzip, try_files, FastCGI params, TCP socket) until a minimal TLS-only config showed the same slowdown. It is a client artifact, not a server property [V]. Real clients are not affected, but any later benchmark in this project must not use `ab` against chunked responses over TLS.

## Findings

1. **FastCGI cache decides it for WordPress.** nginx has it built in and it works in the generated config. Caddy needs a custom binary (`xcaddy build --with github.com/caddyserver/cache-handler`, 52 MB binary, extra supply chain and no distro package) [V].
2. **Caddy's built-in ACME and its error reporting are real advantages**, but both are covered by existing plans (lego, S2 verification) [V for behavior, A for the conclusion].
3. **nginx traps that a generator must avoid:** `add_header` in a location drops all inherited `add_header` lines (lost `Alt-Svc`), and the default worker user of the nginx.org package is `nginx`, which fails with `getpwnam("nginx") failed` if the package user does not exist [V, both hit while testing].
4. **Caddy trap:** RE2 has no lookahead, so rewrite recipes copied from the nginx world need translation [V].
5. **HTTP/3 works in both** with the same Go client. For nginx it requires the nginx.org package (or at least 1.25), which ADR-0005 already assumes [V].
6. **Packaging:** the Ubuntu 24.04 archive has Caddy 2.6.2 [V: `apt-cache policy`]. Current Caddy needs the official repository or a vendored binary [U: Cloudsmith repo reachable, not installed here].

## What would change the recommendation

- A plan to ship automatic HTTPS **without** an own ACME client (Caddy wins there) [A].
- A decision that page caching is out of scope for v1, which removes finding 1 [A].
- A requirement to configure everything over a local JSON API instead of files (Caddy is better suited, nginx would stay file based) [A].

## Not covered

- Caddy driven through its JSON API instead of a Caddyfile, and Caddy rollback behavior in the S2 generation model.
- Real certificates from a public CA, renewal, OCSP.
- PHP applications other than a one-line script, per-site `open_basedir`, FPM pool per site.
- Memory with hundreds of sites, long-running soak tests, logging and log rotation.
- The nginx ACME module: not present in this build (`nginx -V` shows no acme module) [V]. Whether newer nginx packages ship one was not checked [U].

## How to run

```sh
# one time: tools (all under /tmp/s3, nothing in /etc is touched)
apt-get install -y php8.3-fpm apache2-utils
mkdir -p /tmp/s3/bin && cd spike/s3-webserver
go build -o /tmp/s3/bin/gen ./gen && go build -o /tmp/s3/bin/h3check ./h3check && go build -o /tmp/s3/bin/backend ./backend
GOBIN=/tmp/s3/bin go install github.com/caddyserver/caddy/v2/cmd/caddy@v2.11.9
# nginx.org stable: download the .deb from nginx.org and `dpkg -x` it to /tmp/s3/nginxorg

harness/setup.sh                       # certificate, 22 docroots, php-fpm pool
php-fpm8.3 -y /tmp/s3/work/run/php-fpm.conf; /tmp/s3/bin/backend &
mkdir -p /tmp/s3/cfg && /tmp/s3/bin/gen -out /tmp/s3/cfg      # add -cache for the nginx FastCGI cache
# start one server, then:
harness/bench.sh nginx nginx           # or: harness/bench.sh caddy caddy
harness/reload-test.sh                 # nginx vs. Caddy on a busy port and a bad directive
```

`h3check -proto h1|h2|h3 -host site01.test -path /` prints status, protocol, selected headers and body.
