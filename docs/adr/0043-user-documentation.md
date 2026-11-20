# ADR-0043 User documentation in the GitHub wiki

**Status:** Accepted (Kim, 2026-11-20). **Date drafted:** 2026-11-20. **Decides:** Kim. **Related:** ADR-0002 (documentation is CC-BY-4.0), ADR-0037 (UI redesign, screens still change); clean-room rule: no CloudPanel texts ([research](../01-cloudpanel-research.md)).

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
| **A. GitHub wiki, pages written in the repository under `docs/wiki/` and mirrored by CI** | What Kim wants (the wiki); pages go through PRs with markdownlint, lychee and REUSE; nothing to host; no new tool | Edits made in the wiki web editor are overwritten by the next sync; wiki search is basic [U] |
| B. GitHub wiki, edited directly | No sync step | No review, no lint, no link check, no SPDX headers; the wiki is a separate git repository that this project's automation cannot reach today [V: push to `maki-it/hostashi.wiki` refused in this session] |
| C. Static site generator (mdBook 0.5.4, checked on 2026-11-20 [V]) | Offline search, own domain | Needs hosting; Kim prefers the wiki [V: Kim, thread, 2026-11-20] |

## Recommendation

**Option A.** Decided by Kim (thread, 2026-11-20): the repository wiki, with pages written here and mirrored by CI. Writing the pages in the main repository keeps the existing checks and review; the wiki is the published copy.

- **Sync.** A workflow `wiki.yml` runs on pushes to `main` that touch `docs/wiki/**`. It clones `<repo>.wiki.git` with the job's `GITHUB_TOKEN` (`contents: write`), replaces its content with `docs/wiki/`, commits as `github-actions[bot]` and pushes. Plain `git`, no third-party action [U: `GITHUB_TOKEN` can push to the wiki of its own repository].
- **One-way.** The wiki says on every page (footer) that it is generated from `docs/wiki/` and that changes go through a PR. The workflow does a full replace, so stray web edits do not linger.
- **Wiki conventions.** File names are page titles with hyphens (`Install.md` → "Install"); `_Sidebar.md` is the navigation and `_Footer.md` the footer; links between pages use the page name without `.md` [U: GitHub wiki behaviour]. The link checker gets an exclude rule for these wiki-style links if needed.
- **Visibility.** While the repository is private, only people with access to it can read the wiki [U]. That is fine until publication.
- **Place.** `docs/wiki/`. The planning documents stay where they are; the wiki is for people running Hostashi, not for contributors.
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
- **CI.** The existing markdownlint and lychee jobs already cover `docs/wiki/**/*.md`.

## Consequences

- One new workflow with write access to the wiki only; no new tool, no runtime dependency and no new `os/exec` exception.
- Every user-facing feature PR should update the matching wiki page; CONTRIBUTING.md says so.

## Risks to name openly

- **Docs drift.** A manual nobody updates is worse than none. Mitigation: the rule in CONTRIBUTING.md and reference pages generated from code later where possible (installer options, CLI) [A].
- **No versioning.** The wiki shows the state of `main`, which can be ahead of the latest release. Pages mark features that are not released yet [A].
- **Lost web edits.** Someone editing in the wiki web UI loses the change on the next sync. The footer says so.

## Open questions

None. Decided by Kim (thread, 2026-11-20): the GitHub wiki, with pages written in the repository and mirrored by CI.
