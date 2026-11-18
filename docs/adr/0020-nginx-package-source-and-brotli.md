# ADR-0020 nginx package source and brotli

**Status:** Accepted (2026-11-18, by Kim). **Date drafted:** 2026-11-18. **Decides:** Kim. **Related:** ADR-0005 (web server, Accepted), ADR-0009 (target platforms), ADR-0017 (presets, compression and HTTP/3 defaults), ADR-0019 (PHP package source). **Evidence:** Spike S11 ([`spike/s11-brotli/REPORT.md`](../../spike/s11-brotli/REPORT.md)).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

ADR-0005 stays valid for the choice of nginx. This ADR only changes one consequence of it: where the nginx package comes from.

## Context

- Kim wants brotli and HTTP/3 on by default (ADR-0017) and does not insist on Debian if Ubuntu offers it [V: Kim, thread, 2026-11-18].
- ADR-0005 assumed the nginx.org stable repository because Ubuntu 24.04's nginx 1.24 has no HTTP/3 [V: S3].
- The nginx.org repository has no brotli module. Its modules are `acme`, `geoip`, `image-filter`, `njs`, `otel`, `perl`, `xslt` [V: S11].
- The brotli modules of Debian and Ubuntu (`libnginx-mod-http-brotli-filter` and `-static`) depend on the ABI of the distribution's own nginx, so they cannot be used with the nginx.org build [V: S11, package indexes].

## What the distributions offer [V: S11, package indexes and `debian/rules` of the source packages, 2026-11-18]

| Distribution | nginx | Built with `--with-http_v3_module` | Brotli modules | ABI matches |
|--------------|-------|-------------------------------------|----------------|-------------|
| Debian 13 | 1.26.3 | yes | yes (1.0.0~rc-6) | yes, `nginx-abi-1.26.3-1` |
| Ubuntu 26.04 LTS | 1.28.3 | yes | yes (1.0.0~rc-7) | yes, `nginx-abi-1.28.3-1` |
| Ubuntu 24.04 LTS | 1.24.0 | no | yes (1.0.0~rc-5) | yes, but no HTTP/3 |

The build flag shows that the module is compiled in. Spike S12 ([`spike/s12-nginx-distro/REPORT.md`](../../spike/s12-nginx-distro/REPORT.md)) then ran both builds in fresh containers and verified real HTTP/1.1, HTTP/2 and HTTP/3 requests with brotli, gzip and pre-compressed `.br` responses [V: S12, amd64, no VM or firewall].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Distribution nginx plus the distribution's brotli modules (Debian 13, Ubuntu 26.04)** | Brotli and HTTP/3 together, no third-party repository, no own build, security fixes through the normal distro channel, one package source for nginx, PHP (ADR-0019) aside | Older nginx version than nginx.org stable (1.26 and 1.28 against 1.30 [V: S3]). New features arrive with the next distribution release. Ubuntu 24.04 cannot offer HTTP/3 |
| B. nginx.org plus own brotli build | Newest nginx | Own build and release pipeline per nginx version and architecture, a third-party module to patch [A]. Conflicts with the boring-tech rule |
| C. nginx.org, gzip only | Nothing to build, newest nginx | No brotli, which Kim wants |
| D. Keep ADR-0005's assumption unchanged | No work | Same as C |

## Recommendation

**Option A.** Use the distribution's nginx and its brotli modules on Debian 13 and Ubuntu 26.04. Debian 13 stays Kim's first choice from ADR-0009, Ubuntu 26.04 moves from "later" to supported, since it is the newer base [A: fits the ADR-0009 plan to follow later releases].

Ubuntu 24.04 is the open point: its nginx has no HTTP/3. Either it is dropped for v1, or it runs with nginx.org and gzip only, which means a second package source and a different feature set for one distribution. Recommendation: **drop Ubuntu 24.04 for v1**, because every fresh server can choose the distribution freely (Hostashi installs only on fresh servers, ADR-0009) and one package source keeps the installer and the tests simpler [A].

## Consequences

- ADR-0005, consequence "Packages": the nginx.org stable repository is no longer the source. ADR-0005 gets a one-line note that this ADR replaces that bullet once accepted.
- The installer (spike S5) currently sets up the nginx.org apt source with a pinned key and unattended-upgrades rules [V: S5]. That part is removed. nginx then comes from the normal distribution archive and updates through the standard security channel [A].
- The nginx version differs per distribution (1.26.3 and 1.28.x). The `WebServerDriver` renders and validates against the installed version with `nginx -t` (ADR-0005), so templates must avoid directives newer than 1.26 [A]. A version check on the CI matrix covers this.
- Defaults from ADR-0017 apply as written: HTTP/3 on (UDP 443 opened by installer and firewall) and brotli with gzip as fallback for clients that do not accept `br`.
- The `nginx-module-acme` module on nginx.org is not available with distribution nginx. ADR-0003 plans lego for ACME, so nothing is lost [V: ADR-0003].
- The S5 installer must be re-run on Debian 13 and Ubuntu 26.04 with the new source (HTTP/3 request test, brotli response header check). Ubuntu 26.04 was not yet tested in S5 [V: S5 report covers Debian 13 and Ubuntu 24.04].

## Risks to name openly

- The distribution nginx is older and has fewer new features. Security fixes are backported by the distribution [U]. If a needed nginx feature appears only in 1.30, this ADR has to be reopened.
- Distribution brotli modules are version `1.0.0~rc`, a release candidate of ngx_brotli (the upstream project has had little activity) [U]. Maintenance depends on the distribution.
- The installed nginx ABI and the brotli module must stay in step. Both come from the same archive, so a normal upgrade updates both [A].

## Open questions

None. Kim agreed on 2026-11-18 to the recommendation: Ubuntu 24.04 is dropped for v1, and the nginx version difference between Debian 13 (1.26.3) and Ubuntu 26.04 (1.28.x) is acceptable. Still to do: the S5 installer test on Ubuntu 26.04 (the real HTTP/3 and brotli requests are done, see S12).
