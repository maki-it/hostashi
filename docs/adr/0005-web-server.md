# ADR-0005 Web server

**Status:** Accepted (2026-11-18, by Kim). **Date drafted:** 2026-11-18. **Decides:** Kim. **Related:** ADR-0003 (stack), ADR-0006 (state and reconciler), ADR-0009 (fresh servers, Debian 13), ADR-0015 (resource limits). **Evidence:** spikes S2 ([`spike/s2-reconciler/REPORT.md`](../../spike/s2-reconciler/REPORT.md)) and S3 ([`spike/s3-webserver/REPORT.md`](../../spike/s3-webserver/REPORT.md)).

## Context

- Hostashi renders web server config from its declared state and switches it atomically with rollback (ADR-0006, `04-architektur.md` 4.3). The web server sits behind a `WebServerDriver` interface (Render, Validate, Activate, Verify) [V: `04-architektur.md` 4.5, S2].
- The main workload is PHP sites, WordPress first, behind PHP-FPM, plus reverse proxy for Node and Python apps [V: `02-vision-scope.md`].
- Kim runs 22 sites on one server today [V: Kim, thread, 2026-11-17].
- Boring tech is a project rule: prefer proven components [V: project instructions].

## Owner input (2026-11-18)

- After S3: "dann nginx" [V: Kim, thread].
- Kim asked about LiteSpeed. The assessment in the options table is from general knowledge, not tested, and Kim agreed to nginx afterwards [V: Kim, thread].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **nginx** (nginx.org stable) | Native FastCGI cache. Largest pool of recipes (WordPress, Laravel, Magento, Shopware). HTTP/3 (nginx.org, 1.25 and later). Small footprint: 35 MiB idle with 22 sites [V: S3]. Fast reload (about 35 ms) [V: S3] | No built-in ACME (lego, see ADR-0003). Reload errors are silent for bind conflicts (solved by S2 verification). `add_header` inheritance trap [V: S3] |
| **Caddy** | Built-in ACME and HTTP/3, short config (49-line template against 76), reload reports bind conflicts at once, WebSocket and `php_fastcgi` work without recipes [V: S3] | FastCGI cache only through a third-party module and a custom build [V: S3]. 56 MiB idle, about a third of nginx's throughput in this setup [V: S3, indicative]. RE2 regex has no lookahead, so nginx recipes need translation [V: S3]. Distro packages are old (Ubuntu 24.04: 2.6.2) [V: S3] |
| **LiteSpeed Enterprise** | Fast PHP (LSAPI), LSCache for WordPress, reads `.htaccess` [U] | Proprietary, license fees, license key on every server [U]. Conflicts with an AGPL project that installs on fresh servers without registration |
| **OpenLiteSpeed** | Open source (GPL), LSAPI and LSCache [U] | Own configuration model and web admin, fewer recipes and a smaller community, one vendor, not tested here [U] |
| **Apache httpd** | Mature, `.htaccess` support | Heavier per connection, `.htaccess` conflicts with generated config, no advantage for Hostashi [A] |

## Recommendation

**nginx from the nginx.org stable repository as the first and only v1 driver**, behind the `WebServerDriver` interface.

Reasons, in order of weight:

1. The FastCGI cache is built in and tested [V: S3]. A page cache for WordPress is a core hosting feature, and Caddy would need a custom binary with an extra supply chain.
2. The weaknesses of nginx that Caddy does not have are covered by plans we already have: lego for ACME (ADR-0003) and the S2 verification for silent reload failures [V: S2].
3. Smaller footprint and the widest base of known recipes, which matters for a single maintainer [V: S3, A for the maintainer argument].
4. The driver interface keeps a second driver possible later (Caddy if built-in ACME becomes the priority, LiteSpeed only if customers ask) [A: only rendering was compared, no second driver was built].

## Consequences

- **Packages:** nginx.org stable repository on Debian 13 and Ubuntu 24.04, because the distribution packages for Ubuntu 24.04 (1.24) have no HTTP/3 [V: S3]. The `.deb` depends on this repository or vendors the signed packages [A, decide in S5].
- **Reload verification is mandatory** in the reconciler: `nginx -t`, then reload, then proof that the new generation answers, with eight consecutive matches and a site health check; otherwise rollback. A bind conflict passes `-t` and a reload that exits 0 [V: S2, S3]. Changed in `04-architektur.md` 4.3.
- **Generator rules:** per-site snippets live in their own include file (an unmatched `}` is rejected per file [V: S2]). Never add `add_header` in a location without repeating the inherited headers. Set the worker user explicitly (the nginx.org package defaults to a user `nginx` that must exist) [V: S3].
- **HTTPS:** certificates through lego as a library, nginx reads them from files. Renewal triggers a validated reload [A: not built yet].
- **Cache:** `fastcgi_cache` per site with a key and a purge operation in the agent. The design is a later ADR [A].
- **Benchmarks:** do not use `ab` against chunked responses over TLS, it stalls and shows a 10x error [V: S3].

## Risks to name openly

- S3 numbers are single runs on a shared sandbox and only show orders of magnitude [V: S3].
- Real ACME, real ports 80 and 443 and Debian 13 packages were not tested. S5 (installer) must cover Debian 13 and the nginx.org repository.
- nginx has a history of memory-safety CVEs in rarely used modules [U]. The mitigation is a minimal module set and the nginx.org stable branch with automatic security updates (to be defined with the installer).
- Locking the project to nginx syntax for user snippets limits a later driver change, unmanaged snippets would need translation [A].

## Open questions

None for the choice. Remaining work that follows from it: the cache design, the snippet policy (what a custom snippet may do, see S2 finding 3) and the repository setup in S5.
