# ADR-0043 User documentation: tool, place and structure

**Status:** Proposed. **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0002 (documentation is CC-BY-4.0), ADR-0037 (UI redesign, screens still change); clean-room rule: no CloudPanel texts ([research](../01-cloudpanel-research.md)).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim wants proper end-user documentation for admins, managers and users of the panel [V: Kim, thread, 2026-11-20].
- Today `docs/` holds planning documents and ADRs only: research, scope, features, architecture, security, roadmap, decisions [V: `docs/`]. There is no install guide, no page about sites, backups or recovery. `README.md` still describes the project as a skeleton [V].
- Documentation in the repository is already checked: markdownlint and lychee run on every `.md` change, and REUSE marks `docs/**` and `**/*.md` as CC-BY-4.0 [V: `.github/workflows/docs.yml`, `REUSE.toml`].
- The panel UI is being redesigned right now (ADR-0037), so screenshots taken today would be outdated within days [V: PRs 201–206].
- CloudPanel's docs have no license and must not be reused, not even as a structure template [V: `docs/01-cloudpanel-research.md`].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. mdBook 0.5.4** (Rust, single binary, MPL-2.0) | One static binary, no npm or Python tree; plain Markdown with a `SUMMARY.md` as the table of contents; built-in offline search, dark theme, print view; used for the Rust books for years [U] | Plain look; no doc versioning; few plugins (we need none) |
| B. Starlight 0.43.1 (Astro, npm) | Polished look, i18n, components | Large npm dependency tree for docs; still 0.x with frequent breaking releases [U]; second JS build next to `web/` |
| C. Zensical (successor of Material for MkDocs) | Familiar MkDocs layout | Young project; Material for MkDocs is in maintenance mode since late 2025 [U] |
| D. Markdown in the repository only, no generator | Nothing to add | No search, no navigation; hard for end users |

Versions checked on 2026-11-20: mdBook 0.5.4 (crates.io, released 2026-07-06), `@astrojs/starlight` 0.43.1 (npm) [V].

## Recommendation

**Option A, mdBook.** It is the boring choice: one pinned binary, Markdown files that the existing lint, link check and REUSE rules already cover, and nothing that ships in the product.

- **Place.** `docs/manual/` with `book.toml` and `src/`. The planning documents stay where they are; the manual is for people running Hostashi, not for contributors.
- **Audience.** Each page says who it is for: server admin (shell, installer), panel admin, manager, user (ADR-0035 roles).
- **Structure** (first version):
  1. Introduction: what Hostashi is and is not (v1 scope).
  2. Install: requirements (Debian 13, Ubuntu 26.04, fresh server), `install.sh` and its options, DNS for the panel name, port 8443 or 443, firewall in front.
  3. First steps: setup page, first admin, second factor (TOTP, passkeys).
  4. Sites: create, domains and aliases, HTTPS and certificates, PHP version and settings, nginx snippets, maintenance page, disable and delete, parked domains.
  5. Databases: create, users, import and export.
  6. Backups: Storage Box, schedule, restore.
  7. Cron jobs, SSH and SFTP keys, resource limits and presets.
  8. Users and roles: invite, roles, second factor policy.
  9. Moving from CloudPanel: scan, plan, transfer, DNS cut-over, switching the old server off.
  10. Server: firewall, fail2ban, PHP versions, health page (`hostashi doctor`), metrics, API tokens.
  11. Troubleshooting and recovery: locked out (`hostashi user recover`), certificate issues, failed sites, logs.
  12. Reference: command line, installer options, file locations, ports.
- **Writing rules.** English, task-oriented ("how do I …"), short steps, one page per task. Every page has an SPDX header (CC-BY-4.0). Written from Hostashi's own behaviour and code; nothing taken from CloudPanel's docs.
- **Screenshots.** Not before the redesign (ADR-0037) is done. Then taken automatically with the existing Playwright e2e setup, so they can be renewed with one command instead of by hand [A].
- **CI.** `docs.yml` gets a job that downloads the pinned mdBook release, checks its SHA-256 and runs `mdbook build`; the existing markdownlint and lychee jobs cover `docs/manual/**/*.md` already.
- **Publishing.** Not part of this ADR. The repository is not public yet; when it is, a separate step (with Kim's approval) puts the built site on GitHub Pages or `docs.hostashi.de` [A].

## Consequences

- New build tool in CI only; no new runtime dependency and no new `os/exec` exception.
- The license workflow checks Go and npm only; mdBook is MPL-2.0, which is on the allowlist [U]. Its built-in theme ships fonts and scripts into the generated site; their licenses must be listed when the site is published [U].
- Every user-facing feature PR should update the matching manual page; CONTRIBUTING.md says so.

## Risks to name openly

- **Docs drift.** A manual nobody updates is worse than none. Mitigation: the rule in CONTRIBUTING.md and reference pages generated from code later where possible (installer options, CLI) [A].
- **No versioning.** mdBook builds one version. Fine until the first release; after that the published site follows the latest release [A].

## Open questions

1. mdBook as recommended, or a nicer-looking tool with more dependencies (Starlight)?
