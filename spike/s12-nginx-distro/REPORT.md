# Spike S12 report: distribution nginx with brotli and HTTP/3

Status: throwaway spike, nothing here is production code. Evidence tags as in the ADRs: **[V]** verified by a test in this spike, **[A]** assumption, **[U]** unverified.

Question (ADR-0020): do the distribution's nginx and its brotli modules really serve brotli over HTTP/2 and HTTP/3 on Debian 13 and Ubuntu 26.04?

Environment: fresh `debian:trixie` and `ubuntu:26.04` containers (x86_64), run on 2026-11-18 against the live package archives. The packages are installed with apt in a container, nginx runs there as root, and a small Go client (quic-go, certificate checks off on purpose) makes the requests from the same container over loopback. No systemd, no firewall, no real network path.

## Result

**Yes, on both distributions. [V]**

| | Debian 13 | Ubuntu 26.04 |
|---|-----------|--------------|
| nginx | 1.26.3 | 1.28.3 |
| Built with `http_v3` (`nginx -V`) | yes | yes |
| Brotli modules enabled after `apt install` | `50-mod-http-brotli-filter.conf`, `50-mod-http-brotli-static.conf` (no manual `load_module`) | same |
| `nginx -t` with `listen 8443 quic reuseport`, `http2 on`, `http3 on` | ok | ok |
| HTTP/1.1, HTTP/2 and HTTP/3 answer 200 | yes | yes |
| `Accept-Encoding: br, gzip` gives `Content-Encoding: br` (h2 and h3) | yes | yes |
| `Accept-Encoding: gzip` gives gzip, none gives identity | yes | yes |
| Pre-compressed `static.html.br` served with `brotli_static on` | yes | yes |
| Incompressible file (200 KB random) stays uncompressed | yes | yes |
| `Vary: Accept-Encoding` set on compressed responses | yes | yes |
| Critical or emergency entries in the error log | 0 | 0 |

Both runs gave identical byte counts for the same test file, so the numbers below hold for both.

### Size on the test file

The file is synthetic and highly repetitive (3,000 similar HTML lines, 229,893 bytes). The ratio is therefore **not representative** of real WordPress output [A: measure on real pages before quoting savings].

| Encoding | Bytes |
|----------|-------|
| none | 229,893 |
| gzip level 5 | 8,220 |
| brotli level 5 (on the fly) | 4,160 |
| brotli level 11 (pre-compressed `.br`) | 4,216 (served as is) |

(`brotli -q 11` gave a slightly larger file than level 5 here, which happens on small repetitive inputs and says nothing about real pages.)

## What this confirms for ADR-0020

- The distribution nginx plus `libnginx-mod-http-brotli-filter` and `-static` is enough. No own build, no third-party repository [V].
- HTTP/3 works on the same build, so brotli and HTTP/3 can be on by default on both distributions (ADR-0017) [V].
- The Alt-Svc advertisement and the quic listener on the same port as the TCP listener work with one `server` block [V].

## Findings

1. `brotli_types` must not list `text/html`: nginx always compresses it, and a duplicate gives a `duplicate MIME type` warning (not an error) [V]. The generator should leave `text/html` out.
2. HTTP/3 clients see `vary: accept-encoding` in lower case, HTTP/1.1 and HTTP/2 the capitalised form. This is the same header [V].
3. `reuseport` was set on the quic listener only. With several `server` blocks, nginx allows `reuseport` once per address and port, so the generator must emit it exactly once per port [A: documented nginx behaviour, not tested with several blocks here].

## Not tested

- arm64 (amd64 only), a real VM with systemd and a real firewall (UDP 443 must be open for HTTP/3 to work from outside), a real browser, packet loss.
- The S5 installer on Ubuntu 26.04, and the installer with the nginx.org source removed.
- Several `server` blocks and the generated per-site templates with HTTP/3 and brotli.
- Real WordPress responses, CPU cost of brotli at higher levels, and the ngx_brotli `1.0.0~rc` code under load [U].
