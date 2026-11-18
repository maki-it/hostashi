# ADR-0017 CloudPanel import as migration, and Hostashi presets

**Status:** Proposed (not accepted). **Date drafted:** 2026-11-18. **Decides:** Kim. **Related:** ADR-0001 (clean-room), ADR-0005 (nginx), ADR-0006 (state and reconciler), ADR-0009 (target platforms), ADR-0015 (resource limits). **Evidence:** Spike S6, [`spike/s6-adopt/REPORT.md`](../../spike/s6-adopt/REPORT.md).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- S6 built a read-only scanner and ran it twice on Kim's CloudPanel server `web2` [V]. Second run: 21 sites, **0 clean, 19 review, 2 blocked**. Every site carries 10 to 43 directives that Hostashi does not model, 399 in total, and about 90 % of them fall into fewer than ten recurring patterns [V for counts, E for the share].
- Kim's decision on the S6 abort criterion (2026-11-18): a site counts as adopted when it has **no blocker** and every custom part is checked [V: Kim, thread]. Under that reading `web2` reaches 90 %, so the importer stays on the roadmap.
- Kim also asked **not to copy the recurring rules 1:1** into presets, but to decide per rule what makes sense and what should be improved or rebuilt [V: Kim, thread]. This matches the clean-room rule (ADR-0001): we model behaviour, we do not reproduce template text.
- Hostashi is installed only on fresh servers [V: ADR-0009, ADR-0015 context]. So "import" means moving sites from an old server to a new Hostashi server, not taking over the old one in place.
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
| `pagespeed` (18) | The nginx PageSpeed module is no longer maintained upstream [U] | **Drop:** Hostashi does not ship the module. The importer reports it, the review confirms dropping it. Static caching and compression cover part of the effect [A]. |
| `try_files` at server level (14) | Standard for the site type | **Map** to the site type (scanner gap, fixed in the next scanner version). |
| Two-tier front and backend blocks (most sites), cache tier (1) | A pattern of the old setup, not of the site | **Collapse** into one Hostashi site. A cache in between (probably Varnish [A]) is not part of v1; the review notes it. |
| Basic auth (2), `rewrite` (8), `error_page` (3), `set`, `map`, `limit_req_zone` (few) | Individual | Basic auth per path as a typed feature. The rest stays a custom snippet. |

## Consequences

- The importer becomes a Phase-1 feature with four parts: scan (exists as a spike), plan and review screen, data transfer (files, databases), DNS cut-over per site. Effort: several weeks [E, no team calibration yet].
- The presets are product features in their own right and help new sites too, not only imported ones.
- Each preset needs a short spec (settings, defaults, rendered behaviour, tests) before it is implemented. The importer then only needs a mapping from detected behaviour to preset settings.
- Custom snippets remain a supported escape hatch. That means some unvalidated nginx text lives in the state; the S2 candidate check keeps a broken snippet from taking a site down [V: S2], but it does not judge whether a snippet is safe. This is a conscious trade-off.
- Not covered yet: database discovery (MariaDB users and grants), Node.js and Python process supervision, cron environment, SFTP and SSH users. They are part of the importer scope and need their own design.

## Open questions

1. Is a second server during migration acceptable for Kim's own move (`web2` to a fresh server)? [A: yes, since Hostashi needs a fresh server anyway]
2. Which presets are default-on for new sites, and which only on request?
3. Do we keep a "Varnish" option at all, or rely on nginx FastCGI cache (S3 measured it)?
4. Should the importer offer to keep custom snippets verbatim, or require each one to be rewritten into a preset or dropped before import?
