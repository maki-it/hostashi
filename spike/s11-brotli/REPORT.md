# Spike S11 report: brotli for nginx.org builds

Status: desk check against the live package indexes, nothing was installed or run. Evidence tags as in the ADRs: **[V]** verified in this spike, **[A]** assumption, **[U]** unverified.

Question (ADR-0017, Part 4): is there a maintained brotli module for the nginx.org packages on Debian 13 and Ubuntu 24.04, so that brotli can be the default compression with gzip as fallback?

Checked on 2026-11-18, package indexes of nginx.org, Debian trixie and Ubuntu noble (amd64).

## Result

**No brotli module exists for the nginx.org builds. [V]** But the distributions ship matching modules for their own nginx, and two of the three checked builds have HTTP/3 compiled in (see the addendum). Recommendation: use the distribution nginx with brotli, see [ADR-0020](../../docs/adr/0020-nginx-package-source-and-brotli.md). The original gzip-only recommendation below applies only if the nginx.org build stays.

## Findings

| Source | What it offers | Usable with nginx.org nginx? |
|--------|----------------|------------------------------|
| nginx.org package repository (debian trixie, ubuntu noble) | Modules: `acme`, `geoip`, `image-filter`, `njs`, `otel`, `perl`, `xslt` (plus `-dbg`). **No brotli.** [V] | – |
| Debian trixie | `libnginx-mod-http-brotli-filter` and `-static`, version 1.0.0~rc-6, depends on `nginx-abi-1.26.3-1` [V] | No. The ABI pin ties it to Debian's own nginx 1.26.3, not the nginx.org build [V for the dependency, A for the consequence] |
| Ubuntu noble (universe) | The same two packages, 1.0.0~rc-5build1, depends on `nginx-abi-1.24.0-1` [V] | No, and Ubuntu's nginx 1.24 has no HTTP/3 [V: S3] |
| Build the module ourselves | ngx_brotli compiled against the exact nginx.org version | Possible, but we would ship a compiler toolchain or prebuilt binaries per nginx release and architecture, and rebuild on every nginx update [A] |

Side finding: the nginx.org repository ships `nginx-module-acme` [V]. This is relevant to ADR-0003, which plans lego for ACME. It is not evaluated here and not a recommendation; it would only be worth a separate look.

## Options for Kim

| Option | Pro | Contra |
|--------|-----|--------|
| **A. gzip by default, brotli later (recommended)** | Boring tech, no extra build step, HTTP/3 from nginx.org stays | No brotli on first release. gzip is about 15 to 20 % larger on text than brotli at similar speed [U] |
| B. Distribution nginx instead of nginx.org | Brotli from the distro on both systems | Changes ADR-0005. Ubuntu 24.04 has no HTTP/3. Debian's nginx 1.26.3 may have HTTP/3, not checked [U] |
| C. Build ngx_brotli ourselves | Brotli and HTTP/3 together | A build and release pipeline per nginx version and architecture, and a security-update duty for a third-party module [A] |
| D. Brotli at a different layer (CDN, such as Cloudflare, in front of the site) | No work on the server | Not under Hostashi's control, so no default |

## Not tested

- Whether Debian 13's nginx is built with HTTP/3 (running the downloaded binary was not done in this spike).
- Real size and CPU difference between gzip and brotli on typical WordPress output.
- Whether nginx.org plans a brotli module.

## Addendum: distribution builds (Kim's follow-up question, 2026-11-18)

Checked in the package indexes and in `debian/rules` of the source packages. No binary was run [A: build flag is not a run test].

| Distribution | nginx | `--with-http_v3_module` | Brotli modules (ABI matches) |
|--------------|-------|--------------------------|------------------------------|
| Debian 13 | 1.26.3 | yes [V] | yes, `nginx-abi-1.26.3-1` [V] |
| Ubuntu 26.04 (resolute) | 1.28.3 | yes [V] | yes, `nginx-abi-1.28.3-1` [V] |
| Ubuntu 24.04 | 1.24.0 | no [V] | yes, but no HTTP/3 |

Debian 13 and Ubuntu 26.04 therefore give brotli and HTTP/3 together from the distribution archive. Option B in the table above (distribution nginx) becomes the recommendation, written up as ADR-0020.
