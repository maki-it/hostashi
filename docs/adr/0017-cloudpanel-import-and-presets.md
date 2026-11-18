# ADR-0017 CloudPanel import as migration, and Hostashi presets

**Status:** Accepted (2026-11-18, by Kim). **Date drafted:** 2026-11-18. **Decides:** Kim. **Related:** ADR-0001 (clean-room), ADR-0005 (nginx), ADR-0006 (state and reconciler), ADR-0009 (target platforms), ADR-0015 (resource limits). **Evidence:** Spike S6, [`spike/s6-adopt/REPORT.md`](../../spike/s6-adopt/REPORT.md).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- S6 built a read-only scanner and ran it twice on Kim's CloudPanel server `web2` [V]. Second run: 21 sites, **0 clean, 19 review, 2 blocked**. Every site carries 10 to 43 directives that Hostashi does not model, 399 in total, and about 90 % of them fall into fewer than ten recurring patterns [V for counts, E for the share].
- Kim's decision on the S6 abort criterion (2026-11-18): a site counts as adopted when it has **no blocker** and every custom part is checked [V: Kim, thread]. Under that reading `web2` reaches 90 %, so the importer stays on the roadmap.
- Kim also asked **not to copy the recurring rules 1:1** into presets, but to decide per rule what makes sense and what should be improved or rebuilt [V: Kim, thread]. This matches the clean-room rule (ADR-0001): we model behaviour, we do not reproduce template text.
- Hostashi is installed only on fresh servers [V: ADR-0009, ADR-0015 context]. So "import" means moving sites from an old server to a new Hostashi server, not taking over the old one in place.
- Owner input on 2026-11-18 [V: Kim, thread]: **keep Varnish** and move it into the same phase as the importer (release 1.0, see Part 3); **drop PageSpeed**; presets are **switched on per site type** (WordPress, TYPO3, generic PHP, static and so on get different defaults, or none).
- Real vhosts on `web2` are two-tier: a TLS server block proxies to a backend server block on a loopback port that runs PHP, on one site with a cache in between [V: S6 second run].

## Part 1: what the importer is

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Migration tool:** scan the old server read-only (live or a mounted disk snapshot), show a plan, the new server pulls files and database dumps | Old server stays untouched until DNS switches, easy rollback, fits "fresh server only" | Needs a second server during the move, DNS cut-over per site |
| B. In-place adopt: install Hostashi on the CloudPanel server and take over | No second server | Two panels writing the same files, quota needs a reboot on a live system (ADR-0015), no clean rollback |
| C. No importer, manual migration with docs | No code | The main reason CloudPanel users would switch is removed [A] |

**Recommendation: A.** The S6 scanner already has no write path at all [V].

## Part 2: when a site counts as adopted

A site is adopted when the scan finds no blocker. Every directive that does not map onto a preset or the site model is carried as a **custom snippet**: it is shown to a person once, goes through the S2 candidate generation with `nginx -t`, and is stored as such so it stays visible later. Nothing from the old server is written to a live config without that check.

## Part 3: presets instead of copied rules

Principles:

1. A preset is a **named, typed feature** with its own settings (on/off, a few values), not a block of nginx text. The reconciler renders it from Hostashi's own templates.
2. The importer maps a recognized **behaviour** to a preset (for example "PHP files under uploads are denied"), not a text match against a known template. What it cannot map stays a custom snippet.
3. Each preset is designed from the nginx documentation and the applications' own docs, written by us (ADR-0001). The table below is a first assessment per pattern, not a finished spec.

| Pattern on `web2` (count) | Assessment | Proposal for Hostashi |
|---------------------------|------------|-----------------------|
| ACME challenge location (21) | Hostashi issues its own certificates | **Drop** on import, Hostashi's ACME owns this path. |
| Hidden-file deny, `.git`, `.ht*`, `.svn` (15) | Several overlapping rules today | **Improve:** one default-on rule "deny all dot paths except `/.well-known/`", answer 404 instead of 403 so the existence of a file is not revealed [A]. |
| Static asset caching (20) | Useful, but one expiry for everything | **Improve:** preset "static caching" with two classes: fingerprinted assets long and `immutable`, other assets short; access log for assets optional [U]. |
| `add_header` in front and backend blocks (141) | Security and cache headers spread over two tiers | **Rebuild:** typed "security headers" setting (HSTS, `X-Content-Type-Options`, `Referrer-Policy`, frame options, CSP opt-in) rendered in one place. nginx drops all inherited `add_header` lines in a block that defines its own [U, well-known pitfall], so Hostashi must render headers so that a location never silently loses them. |
| `if` at server level (70) | Probably host or scheme redirects [A] | **Rebuild** as typed redirects (canonical host, HTTPS, aliases) in their own server blocks, no `if`. An `if` that is not a redirect stays a custom snippet. |
| WordPress hardening: `xmlrpc.php`, login, uploads, config files (about 30) | Valuable, but uneven across sites | **Improve:** preset "WordPress hardening" with toggles. Always on: no PHP execution in uploads, deny config and backup files. Default on but switchable: block `xmlrpc.php` (some apps and plugins need it [U]). Login protection through rate limiting rather than path rules [A]. |
| `pagespeed` (18) | The nginx PageSpeed module is retired upstream (the Apache project carries a `RETIRED.txt`, repositories archived) [V: web check 2026-11-18] | **Drop** (owner decision): Hostashi does not ship the module. The importer reports it, the review confirms dropping it. Static caching and compression cover part of the effect [A]. |
| `try_files` at server level (14) | Standard for the site type | **Map** to the site type (scanner gap, fixed in the next scanner version). |
| Two-tier front and backend blocks (most sites), cache tier (1) | Two tiers without a cache are a pattern of the old setup, not of the site | **Collapse** into one Hostashi site when there is no cache in between. When a cache sits in between (probably Varnish [A]), map it to the Hostashi Varnish option (owner input: Varnish stays and ships with the importer). |
| Basic auth (2), `rewrite` (8), `error_page` (3), `set`, `map`, `limit_req_zone` (few) | Individual | Basic auth per path as a typed feature. The rest stays a custom snippet. |

## Part 4: presets per site type

Owner input: presets are switched on by site type, not globally. The site model keeps its base types (static, PHP, Node.js, Python, reverse proxy); WordPress and TYPO3 are **application profiles** of the PHP type that bring their own preset defaults. The importer picks the profile from what it detects (S6 already recognises WordPress by file existence; TYPO3 detection still has to be built [A]). A person can switch every preset on or off per site afterwards.

First proposal for the defaults (on = active by default, opt = available but off, – = not offered):

| Preset | WordPress | TYPO3 | Generic PHP | Static | Node.js / Python / proxy |
|--------|-----------|-------|-------------|--------|--------------------------|
| Deny dot paths except `/.well-known/` (404) | on | on | on | on | on (at the nginx front) |
| Security headers baseline (HSTS, `nosniff`, `Referrer-Policy`) | on | on | on | on | on |
| Frame options, CSP | opt | opt | opt | opt | opt |
| Static caching (two classes) | on | on | on | on | opt (app may set its own headers) |
| No PHP execution in upload directories | on (`wp-content/uploads`) | on (`fileadmin`, upload folders) [U] | opt (path set per site) | – | – |
| Deny config, backup and dump files (`*.sql`, `*.bak`, `*.env`, editor swap files) | on | on | on | on | – |
| WordPress hardening (`xmlrpc.php` block, login rate limit) | on | – | – | – | – |
| TYPO3 hardening (deny internal folders such as `var/`, `vendor/`, config files) [U, to be specified from TYPO3's own docs] | – | on | – | – | – |
| HTTP/3 (QUIC), see note below | on | on | on | on | on |
| Compression: brotli with gzip fallback, see note below | on | on | on | on | on (text responses only) |
| Varnish cache tier | opt | opt | opt | – | opt |
| Basic auth per path | opt | opt | opt | opt | opt |

Owner input (2026-11-18): HTTP/3 and compression are on by default, and Kim prefers brotli over gzip [V: Kim, thread]. Notes:

- **HTTP/3** is mostly a server-level setting (UDP 443, one QUIC listener shared by all sites, `Alt-Svc` header). The per-site switch only turns the advertisement off. The installer and the firewall rule must open UDP 443, otherwise clients silently fall back to HTTP/2 [A]. nginx.org stable supports it [V: S3].
- **Compression:** the stock nginx.org package has gzip but no brotli [V: S3]. Default is therefore gzip for now. Brotli needs an extra module (the ngx_brotli module or a package built for the nginx.org version) [U, not checked here]. That is a third-party build on a fresh server and conflicts with the boring-tech rule unless a maintained package exists. Proposal: a short spike (S11) checks whether nginx.org or another maintained repository ships a brotli module for Debian 13 and Ubuntu 24.04, including updates in step with nginx. If yes, brotli becomes the default with gzip as fallback for clients that do not accept `br`; if not, gzip stays the default and brotli is a later option [A]. Pre-compressed static files (`.br`, `.gz`) are an alternative worth testing in the same spike [A].

The values are a starting point [A]; each preset spec confirms or changes them. Nothing here is copied from CloudPanel templates (ADR-0001); the TYPO3 and WordPress rules are written from the projects' own security documentation.

## Consequences

- The importer stays a release-1.0 feature (phase `1` in `03-features.md`), together with Varnish with four parts: scan (exists as a spike), plan and review screen, data transfer (files, databases), DNS cut-over per site. Effort: several weeks [E, no team calibration yet].
- The presets are product features in their own right and help new sites too, not only imported ones.
- Each preset needs a short spec (settings, defaults, rendered behaviour, tests) before it is implemented. The importer then only needs a mapping from detected behaviour to preset settings.
- Custom snippets remain a supported escape hatch. That means some unvalidated nginx text lives in the state; the S2 candidate check keeps a broken snippet from taking a site down [V: S2], but it does not judge whether a snippet is safe. This is a conscious trade-off.
- Varnish stays and moves to release 1.0 (owner input): the site model needs an optional cache tier (nginx TLS front, Varnish, nginx backend, PHP-FPM), with its own VCL, purge on deploy or from the CMS, and per-site memory accounting (ADR-0015). This adds a long-running service per server to the reconciler [A].
- Not covered yet: database discovery (MariaDB users and grants), Node.js and Python process supervision, cron environment, SFTP and SSH users. They are part of the importer scope and need their own design.

## Open questions

Accepting this ADR settles Parts 1 to 4 in principle. The questions below are still open unless marked as decided.

1. ~~Is a second server during migration acceptable?~~ Decided by Kim (2026-11-18): yes. Kim starts on a fresh server and shuts the old CloudPanel server down after the migration [V: Kim, thread].
2. ~~Which presets are default-on?~~ Decided: per site type (Part 4). Kim confirmed the default matrix on 2026-11-18, including HTTP/3 and compression as defaults.
3. ~~Do we keep a Varnish option?~~ Decided by Kim: yes, in the same phase as the importer. The Varnish configuration (VCL) and the purge integration must be our own; the CloudPanel WordPress purge plugin is GPLv3 and usable only if ADR-0002 allows it [V: `01-cloudpanel-research.md`].
4. ~~Should the importer keep custom snippets verbatim?~~ Decided by Kim (2026-11-18): yes, after a one-time review by a person; they stay visible as custom snippets (Part 2).
