# ADR-0069 Application profiles (vhost templates) and WordPress quick install

**Status:** Accepted (Kim, 2026-11-25). **Date drafted:** 2026-11-25. **Decides:** Kim. **Related:** ADR-0001 (clean-room), ADR-0004 (web/agent privilege split), ADR-0017 Parts 3 and 4 (presets and application profiles), ADR-0024 (MariaDB per site), ADR-0028 (cron), ADR-0029 (custom snippets), ADR-0033 (hardening), ADR-0046 (page cache), ADR-0049 (PHP settings), ADR-0054 (site types), ADR-0062 (Git deploy), ADR-0070 (staging and cloning, drafted with this one), [03-features](../03-features.md) rows "Vhost templates" and "WordPress quick install (WP-CLI)" (both P1, in 1.0 scope).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- Release 1.0 contains "Vhost templates (Laravel, Symfony, WP, Magento, Shopware, …)" with the note "Custom templates, YAML-based, preview", and "WordPress quick install (WP-CLI)" [V: `03-features.md` 3.0 and 3.1].
- ADR-0017 already decided the shape: no copied vhost text, but **application profiles** of the PHP type (WordPress, TYPO3, …) that switch typed presets on or off; every preset can be changed per site afterwards [V: ADR-0017 Parts 3 and 4]. Only the page cache presets exist in code (`WordPress`, `WooCommerce` cookie and path lists); the hardening presets from the ADR-0017 table (no PHP in uploads, deny config and dump files, `xmlrpc.php`) are not built yet [V: `internal/web/pagecache.go`, no `xmlrpc` rule in `internal/web/nginx.go`].
- The PHP vhost today has one shape for every PHP site: `index index.php …`, `location /` with `try_files $uri $uri/ /index.php$is_args$args`, and `location ~ \.php$` with `try_files $fastcgi_script_name =404` [V: `internal/web/nginx.go`]. The web root can be a folder below `htdocs` (`WebDir`, ADR-0054 Addendum 1) [V: `internal/store/sites.go`]. Most PHP frameworks only need a different web root and a front controller, which this shape already covers [A].
- Free nginx text is refused by design: snippets go through an allowlist that the agent checks again (ADR-0029) [V]. A "YAML template" with nginx text inside would reopen the root problem described there.
- CloudPanel ships vhost templates per application; their repository has no license, so neither text nor structure is taken over (ADR-0001) [V: project instructions].
- Every program a site runs today runs as the site user in a systemd unit started by the agent: PHP-FPM, cron, app units, Git deploy builds, database jobs [V: `packaging/systemd/`, `internal/deployunit`, `internal/dbjob`]. Product code may use `os/exec` only in six reviewed packages (`sysuser`, `webctl`, `phpctl`, `resticctl`, `mariadbctl`, `pgctl`) [V: `.golangci.yml`].
- WP-CLI is MIT licensed [V: its `LICENSE` file, 2026-11-25] and is the usual tool for installing WordPress from a shell [U]. The phar build is published with a SHA-512 file next to it [V: `wp-cli/builds`, 2026-11-25]. It warns when it runs as root and needs `--allow-root` [U].
- WordPress 7.1.5 is current, needs PHP 7.4 or newer [V: `api.wordpress.org/core/version-check/1.7`, 2026-11-25]; wordpress.org publishes `.sha256` and `.sha1` files next to the release archives [V: HTTP 200, 2026-11-25] and per-file MD5 checksums through its API [V].
- Site users have no egress filter, so a unit running as the site user can reach wordpress.org [V: no egress rules in `internal/firewall`].

## Options

**1. What a template is**

| Option | Pro | Contra |
|--------|-----|--------|
| A. Built-in profiles only, defined in Go | Least code, every profile reviewed and tested | No own templates, the 03-features note "custom templates" is not met |
| **B. Built-in profiles plus own profiles as typed data (the same schema, stored in the panel, YAML import/export), no nginx text; extra rules only through the ADR-0029 snippet slots** | Meets "custom templates, YAML-based"; the agent still sees only typed settings and checked snippets | A schema to version; UI for own profiles |
| C. Templates as nginx text with placeholders | Maximum freedom, closest to CloudPanel | Contradicts ADR-0029 and ADR-0004 (root via `access_log`, `fastcgi_pass` to other sockets) |

**2. How WordPress gets installed**

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Pinned WP-CLI in a oneshot unit `hostashi-wp@<site>` as the site user; a small Go runner (`hostashi wp-run`, new package `internal/wpcli`) starts `php wp-cli.phar` with fixed sub-commands and checked arguments, secrets on stdin** | Uses the tool WordPress people know; runs as the site user in the site's slice like a deploy build; the agent never starts WP-CLI itself | Needs a seventh `os/exec` exception (`internal/wpcli`), limited to the site-user unit; one more pinned download to keep current |
| B. The agent runs WP-CLI directly through `os/exec` | Fewer moving parts | A root process starts site-controlled PHP (plugins, `wp-config.php`); against ADR-0004 |
| C. Pure Go: download the archive and its `.sha256`, unpack, write `wp-config.php`, then post the setup form to `wp-admin/install.php` over loopback | No new exception, no WP-CLI | The install form is not an API and can change with any release [A]; no WP-CLI for later steps (search-replace in ADR-0070) |

## Recommendation

1B and 2A.

**Profiles**

- A profile is a named set of typed values: site type (PHP), lowest PHP version, web root folder below `htdocs`, front controller (`index.php` or none), the ADR-0017 presets with their defaults (deny dot paths, deny config/dump files, no PHP in listed upload folders, page cache preset, static caching), PHP setting defaults within the ADR-0049 allowlist, suggested cron jobs (shown, not created), and a short checklist text for the person.
- Built-in profiles for 1.0, each written from the application's own documentation (ADR-0001) with a render test: Generic PHP, WordPress, WooCommerce (WordPress plus the WooCommerce cache preset), Laravel (`public`), Symfony (`public`), Shopware 6 (`public`), TYPO3 (`public`, composer mode), Drupal (`web`) [A: folders from memory, each checked in its spec]. Magento 2 waits: its nginx rules (static file versioning, `setup/`, `pub/`) are long and need their own spec [A].
- Own profiles: an admin copies a built-in profile and changes values; export and import as YAML with a `version` field. No nginx text; a profile may carry snippet text for the two ADR-0029 slots, which goes through the same allowlist on save and again in the agent.
- The site stores its profile name and every value that differs, so a later change of a built-in profile reaches sites that did not override it. Changing the profile of a site only re-renders the vhost, it never touches files.
- Preview: the panel shows the rendered server block for a profile and site (read only), produced by the same renderer as the agent with placeholder paths.
- The CloudPanel import picks the profile from what it detects (today WordPress by file existence [V: ADR-0017]).

**WordPress quick install**

- Offered when creating a PHP site with the WordPress or WooCommerce profile, and on an existing site whose web root is empty. Inputs: site title, admin user name, admin email, language (`en_US`, `de_DE`, later more), "discourage search engines" switch. The admin password is generated and shown once, or typed by the person.
- Steps: the panel creates a database and user for the site (ADR-0024, generated password), then asks the agent to start `hostashi-wp@<site>` with a job file. The runner does `core download` (latest, locale), `config create` (database values, salts from WP-CLI, `DISALLOW_FILE_EDIT` true, `WP_ENVIRONMENT_TYPE` production), `core install`, and `option update` for the permalink structure. Passwords reach the unit as systemd credentials and WP-CLI on stdin, never in argv, a drop-in or a log.
- WP-CLI comes from a pinned version with its SHA-512 in Hostashi's source, installed root-owned to `/opt/hostashi/wp-cli/<version>/wp-cli.phar`, updated by a weekly workflow like the PHP and Node versions. WP-CLI checks the WordPress download against wordpress.org's checksums [U]; the runner runs `core verify-checksums` after the download as a second check.
- The PHP binary is the site's own version; the unit gets the site's PHP settings and limits. WP-CLI never runs as root; the runner refuses uid 0.
- The site's SSH users get a `wp` command on their `PATH` (a root-owned wrapper calling the same phar with the site's PHP), so WP-CLI is also there for their own work.
- After the install Hostashi does not manage WordPress updates; WordPress's own background updates apply [U]. The log of the run is shown in the site's logs (ADR-0053).
- API: `GET /api/v1/profiles`, `POST/PUT/DELETE /api/v1/profiles/{name}` (admin, scope `settings:write`), the profile as a field on site create and update, and `POST /api/v1/sites/{name}/wordpress/install` plus a status endpoint (managers and site users with a grant, scope `sites:write`). All through `mux.Op` (ADR-0058), audited, CLI through `hostashi api`.

## Consequences

- One new `os/exec` exception (`internal/wpcli`), which needs this ADR's approval: it only runs inside `hostashi-wp@<site>` as the site user, with an allowlist of WP-CLI sub-commands and argument checks, and is listed in `.golangci.yml` with that reason.
- The hardening presets from ADR-0017 Part 4 have to be built first (they are part of the profiles); each gets a spec, a hostile test and a render test.
- WP-CLI loads the site's WordPress and plugins, so a later WP-CLI step runs the site's own code as the site user. That is the same trust as PHP-FPM for that site [A].
- One more external download (WP-CLI) and one more weekly bump workflow. WordPress itself comes from wordpress.org at install time, so a quick install needs internet access from the server [V: site users have it].
- Effort [E]: profiles with schema, built-ins, own profiles, preview and UI about 6 to 8 PRs; WordPress install about 4 PRs (pinning and workflow, runner and unit, panel API, UI and wiki).
- Tests: render tests per built-in profile against `nginx -t`, profile YAML round trip and refusal of nginx text outside the snippet slots, runner argument checks and refusal of root, an installer test that installs WordPress on a fresh Debian 13 and Ubuntu 26.04 container and loads the front page.

## Open questions for Kim

1. ~~Templates: built-in profiles only (1A), or built-in plus own typed profiles with YAML import/export (1B)?~~ Decided (Kim, 2026-11-25): 1B.
2. ~~WordPress install: pinned WP-CLI in a site-user unit with a new, narrow `os/exec` exception `internal/wpcli` (2A), or pure Go without WP-CLI (2C)?~~ Decided (Kim, 2026-11-25): 2A, including the new `os/exec` exception `internal/wpcli`.
3. ~~Built-in profiles in 1.0: Generic PHP, WordPress, WooCommerce, Laravel, Symfony, Shopware 6, TYPO3 and Drupal, with Magento 2 after 1.0?~~ Decided (Kim, 2026-11-25): yes, these eight; Magento 2 after 1.0 with its own spec.

The ADR as a whole was accepted by Kim on 2026-11-25.

## Addendum 1: reinstall and detection (2026-11-26)

**Status:** Accepted (Kim, 2026-11-26). **Decides:** Kim (asked for both on 2026-11-26).

### Context

The quick install ran only once per site: after a successful run the card kept saying "installed", even when the person had deleted WordPress in the file manager, and there was no way to start over without emptying the web root by hand [V: thread 2026-11-26].

### Decision

- **Detection.** `site.wordpress.status` looks at the web root (as root, through `os.Root`, never following links): WordPress counts as installed when `wp-config.php` or `wp-includes/version.php` is a regular file; it also reports whether the web root is empty and the `DB_NAME` of `wp-config.php` if it is a plain database name. A successful run whose WordPress is gone reports `none` again, so the form comes back. A failed run keeps its error.
- **Reinstall.** `POST /api/v1/sites/{name}/wordpress/reinstall` with the install fields and `confirm` = the site's name. The job gets `clear`: the runner in `hostashi-wp@<site>` empties the web root **as the site user** before WP-CLI runs, so the agent itself never deletes site files. The install gets a new database (`<site>_wp2` if `<site>_wp` is taken). After the start, the panel drops the old WordPress database only if it is a local database of the same site with a quick install's name (`<site>_wp` to `<site>_wp9`); any other database stays.
- **No automatic backup.** The form warns and links to Backups; backups run per server today, not per site [V: `POST /api/v1/backups/run`].

### Consequences

- Same action and scopes as the install (`sites.wordpress:install` plus `databases:write`), audited as `site.wordpress.reinstall` with the old database.
- If the install fails after the old database was dropped, the person restores from a backup; the warning says so.
