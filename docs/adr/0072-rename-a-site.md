# ADR-0072 Rename a site

**Status:** Accepted (Kim, 2026-11-25). **Date drafted:** 2026-11-25. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0008 (backups), ADR-0015 (limits), ADR-0016 (storage pools), ADR-0018 (REST API), ADR-0028 (cron), ADR-0032 (SSH per site), ADR-0035 (roles), ADR-0054 (site types), ADR-0058 (OpenAPI and CLI), ADR-0061 (Python sites), ADR-0068 (change the domain of a site), ADR-0070 (staging and cloning).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Kim asked that an existing site can be renamed (2026-11-25).
- A site has exactly one name today. It is at the same time the label in the UI, the Linux user and group, the folder of the home (`<pool>/<name>`), the key in the agent's root-owned registry, and the `{name}` in every API path and CLI command [V: `internal/store/sites.go`, `internal/sites/registry.go`, `internal/agentapi/sites.go`]. There is no separate display name [V: migrations 0006 and 0056].
- The name rule is `^[a-z][a-z0-9_-]{1,31}$` because it must be a valid Linux user name [V: `agentapi.ValidateSiteUser`].
- Inside the panel database, site data refers to `sites.id`, not to the name [V: migrations, `site_domains.site_id`]. Outside it, the name is part of many system objects: PHP-FPM pool files and units `hostashi-php…@<name>`, the slice `hostashi-site-<name>.slice`, Valkey units, cron and Git deploy units, app units and the Python venv unit, the file manager unit, nginx config and log folders `/var/log/nginx/sites/<name>`, PHP and app log folders, object cache configs, traffic and usage files, restic snapshot tags, and the crontab spool [V: grep over `internal/`].
- Applications store absolute paths to their home: `wp-config.php` and plugin caches, Composer and Laravel caches, `.env` files, cron commands the person typed, and the Python venv, whose scripts carry the absolute interpreter path in their first line [U for the applications; V for the venv being `<home>/.venv`, ADR-0061]. Moving the home breaks these until they are fixed or rebuilt.
- `usermod -l` refuses to rename a user that has running processes [U]; every PHP, app, Valkey and cron unit of the site runs as that user, so a system rename needs the site stopped.
- SSH and SFTP logins use the site name as user name (ADR-0032); a system rename changes how people and deploy tools log in [V].
- `internal/sysuser` may run `usermod` for the login shell only; a system rename would need `usermod -l -d` and `groupmod -n`, which is a new `os/exec` use and must be covered by an ADR [V: `.golangci.yml`].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Display name: a free label per site, editable at any time; the system name stays** | No root operation, no downtime, nothing in the applications breaks; works for every site type including redirect and proxy sites | The Linux user, home folder, SSH login and API path keep the old name |
| B. Full rename of the system name: stop the site, rename user and group, move the home, rewrite every unit, config, log folder and registry entry, start again | The old name disappears everywhere | Downtime; new `os/exec` (`usermod -l -d`, `groupmod -n`); dozens of places that must all move or the site is half-renamed; absolute paths in applications and the Python venv break [U]; SSH, SFTP and deploy logins change; old backups are tagged with the old name; hard to roll back halfway |
| C. A plus "move to a new system name" later, built on the clone of ADR-0070: clone under the new name, hand the domains over with ADR-0068, delete the old site | Reuses typed, tested parts instead of an in-place rename; the old site stays intact until the person deletes it | Needs ADR-0070 first; the same absolute-path problem as any clone; two sites exist for a while and need space twice |

## Recommendation

**Option A for 1.0**, with C as the path to a new system name after ADR-0070 is built. B is not recommended: it touches almost every part of the agent at once for a cosmetic gain and is the riskiest way to get there.

**Data**

- Migration 0065 adds `sites.title TEXT NOT NULL DEFAULT '' CHECK (length(title) <= 64)`.
- Empty means "no display name"; the UI then shows the system name as today.
- The title is free text: any Unicode letters, digits, spaces and common punctuation, trimmed, at most 64 characters, no control characters and no line breaks. It is display text only; it never reaches the agent, a path, a unit name or a shell [A: keeps the rule "names reaching the agent are validated system names" intact]. React escapes it in the UI; mails (ADR-0044) are plain text.
- Titles need not be unique; the system name stays the unique key.

**API and CLI**

- `GET /api/v1/sites` and `GET /api/v1/sites/{name}` return `title`.
- `PUT /api/v1/sites/{name}/title` with `{ "title": "..." }` sets or clears it (empty string clears). Same permission as other site settings: admins, managers, and users with a write grant on the site (ADR-0035). Audit-logged with old and new value.
- New route with `mux.Op` and an OpenAPI entry (ADR-0058); `hostashi site title <name> <title>` in the CLI, and `--title` on `hostashi site create`.
- Create site and the CloudPanel import accept an optional title.

**UI**

- In the site header a "Rename" action opens a small dialog with the display name field. The dialog says plainly that the system name (Linux user, home folder, SSH login) stays and shows it in code style.
- Site list, sidebar, search, dashboard tiles and the site picker show the title first and the system name smaller next to or below it. Search matches both.
- The browser title and breadcrumbs use the title when set.
- Mails and the audit log keep using the system name, with the title in brackets where it helps [A].

**Later (option C, not in this ADR's work)**

- After ADR-0070 is built, a separate ADR can add "Move to a new system name": clone under the new name, move domains and certificates with ADR-0068, then delete the old site after a typed confirmation.

## Consequences

- No new `os/exec` exception and no new agent operation; the change stays in the panel (store, API, UI).
- The name a person sees and the name on the server can differ. The UI must always show the system name somewhere on the site page, so SSH, SFTP and support questions stay unambiguous.
- One migration (0065), one new route, OpenAPI document update, CLI parity, UI changes in several lists, wiki page update.
- Effort [E]: 1 PR backend (migration, store, API, OpenAPI, CLI, tests), 1 PR UI and wiki with screenshots.
- Tests: migration test, title validation (length, control characters, line breaks, trimming, empty clears), permission test for a read-only grant, audit entry, OpenAPI response check, UI test that the system name stays visible.

## Open questions for Kim

1. ~~Display name only for 1.0 (A), with a real system rename later through clone and domain handover (C)? Or do you need the Linux user and home folder renamed in place (B)?~~ Decided (Kim, 2026-11-25): A for 1.0, C later.

The ADR as a whole was accepted by Kim on 2026-11-25.
