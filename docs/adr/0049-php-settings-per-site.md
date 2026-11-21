# ADR-0049 PHP settings per site

**Status:** Accepted (Kim, 2026-11-21). **Date drafted:** 2026-11-21. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0005 (web server), ADR-0015 (per-site resource limits), ADR-0017 (import and presets), ADR-0019 (PHP), ADR-0029 (nginx snippets), ADR-0033 (hardening baseline), ADR-0035 (roles), ADR-0036 (PHP versions from the panel).

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- `03-features.md` lists "Per-site PHP settings (memory_limit, ...)" as P0, "through the pool config, not through `PHP_VALUE` in nginx" [V]. Kim wants more PHP ini settings per site, from an allowlist only [V: Kim, project chat, 2026-11-20].
- Every site has its own FPM master and pool file, rendered by `internal/php` (ADR-0015). The pool sets only fixed values today: `open_basedir`, the temp and session directories, `allow_url_include`, two session flags and, unless the site may start programs, `disable_functions`, all as `php_admin_value`/`php_admin_flag` [V: `internal/php/php.go`]. Everything else comes from the distribution's `php.ini`: `memory_limit = 128M`, `upload_max_filesize = 2M`, `post_max_size = 8M`, `max_execution_time = 30`, `max_input_vars` 1000, no `date.timezone` [V: `/etc/php/8.3/fpm/php.ini`, Ubuntu package 8.3.6].
- The site vhost sets neither `client_max_body_size` nor `fastcgi_read_timeout`, so nginx's defaults apply: 1 MB request bodies and 60 s [V: `internal/web/nginx.go`; defaults U]. **Today a site cannot take an upload above 1 MB**, whatever PHP allows.
- PHP-FPM reads `.user.ini` files in the site's directories (default `user_ini.filename = ".user.ini"`, cached 300 s) [V: php.ini comments]. Tested with PHP-FPM 8.3.6 and a pool with both kinds of values [V: sandbox test, 2026-11-21]:
  - `php_value[...]` in the pool is a default: a `.user.ini` overrides it, and `ini_set()` changes it.
  - `php_admin_value[...]` is fixed: `.user.ini` and `ini_set()` cannot change it (`ini_set` returns `false`). For `max_execution_time` this also makes `set_time_limit()` fail.
- So a site user can already change most PHP settings with a `.user.ini` today. Only the `php_admin_*` values are boundaries. The real resource boundaries are the slice (memory, CPU, processes, ADR-0015) and nginx (body size, timeout), both set by the panel.
- The CloudPanel scan reads `php_admin_value`/`php_value` settings from each pool, without `env[]` [V: `internal/cloudpanel/scan/pool.go`].

## Options

| Option | Pro | Contra |
|--------|-----|--------|
| **A. Typed allowlist, rendered as `php_value` (defaults the site may still change), hard limits in nginx and the slice** | Behaves like every common host: WordPress' `wp_raise_memory_limit()`, `set_time_limit(0)` in importers and `.user.ini` from security plugins keep working [A]. The panel controls what matters for the server (body size, timeout, memory) where it is enforced anyway | A site can set a higher `memory_limit` than the panel shows; the slice still stops it |
| B. Typed allowlist, rendered as `php_admin_value` (fixed) | The panel value is the only value | Breaks `set_time_limit()` and `ini_set('memory_limit')` [V: test], which many plugins call; it looks like a limit but protects nothing the slice does not already protect [A] |
| C. Free key/value pairs | Covers everything | Lets the panel set `open_basedir`, `disable_functions`, `auto_prepend_file`, `extension` and so on; breaks ADR-0033 and the agent rule. Rejected |

## Recommendation

**Option A.**

- **Allowlist.** A closed list in `agentapi`, each entry with a type, bounds and a Hostashi default. Values are typed fields, never raw text; the pool renders its own formatting (as `internal/snippet` does).

  | Setting | Type and bounds | Default |
  |---------|-----------------|---------|
  | `memory_limit` | size, 32M to 4G, not `-1` | 256M |
  | `upload_max_filesize` | size, 1M to 2G | 64M |
  | `post_max_size` | size, 1M to 2G, at least `upload_max_filesize` | 64M |
  | `max_execution_time` | seconds, 10 to 3600 | 60 |
  | `max_input_time` | seconds, 10 to 3600 | 60 |
  | `max_input_vars` | 1000 to 100000 | 3000 |
  | `max_file_uploads` | 1 to 500 | 20 |
  | `date.timezone` | IANA name from the system's zone database | UTC |
  | `display_errors` | on/off | off |
  | `error_reporting` | one of three presets: production (`E_ALL & ~E_DEPRECATED`), development (`E_ALL`), errors only (`E_ERROR \| E_PARSE \| E_CORE_ERROR \| E_COMPILE_ERROR`) | production |
  | `session.gc_maxlifetime` | seconds, 60 to 30 days | 1440 |
  | `short_open_tag` | on/off | off |

  Defaults and bounds are [E]; 256M, 64M and 3000 follow common WordPress and WooCommerce advice [U].
- **Never offered.** `open_basedir`, `disable_functions` (only through the existing "programs" switch), `allow_url_include`, `allow_url_fopen`, temp and session paths, `auto_prepend_file`/`auto_append_file`, `extension`/`zend_extension`, `sendmail_path` and `mail.*` (ADR-0044), `opcache.*` (later, see Consequences). These stay `php_admin_*` where the pool sets them, so neither the panel nor `.user.ini` changes them [V: test].
- **nginx follows PHP.** The site vhost gets `client_max_body_size` = `post_max_size` and `fastcgi_read_timeout` = `max_execution_time` + 10 s (at least 60 s). That also fixes today's 1 MB limit. A higher value in a `.user.ini` does not get past nginx [A].
- **Memory hint.** If the site has a memory limit (ADR-0015) and `memory_limit` × PHP workers exceeds it, the panel shows a warning, it does not refuse; PHP workers rarely all use their maximum at once [A].
- **`.user.ini` stays on.** Security and cache plugins write it [U]. The panel says that a `.user.ini` in the site can change these values, and that the server's limits (upload size, timeout, memory) still apply.
- **Agent.** `php.Pool` gets a `Settings` field; the agent checks it again against the allowlist, renders `php_value[...]` lines, runs `php-fpm -t` and reloads, with the existing rollback. The vhost values come with the next `web.apply`. No new agent operation type beyond the pool and vhost fields, no new `os/exec`.
- **API.** `GET /api/v1/sites/{name}/php/settings` returns the values, the defaults and the allowlist with bounds (for the form); `PUT` takes only changed keys, `422 php_setting_rejected` names the key and the reason. An empty value means "Hostashi default".
- **Roles.** The settings change only the site itself, and the site could set most of them in `.user.ini` anyway, so `user` may change them for its own site (same as the PHP version, `sites.php:write`) [V: Kim, 2026-11-21].
- **Import.** Allowlisted keys from a CloudPanel pool are taken over (values out of bounds are clamped and named in the review); all other keys are listed as "not taken over". The vhost's `client_max_body_size` is compared with `post_max_size` and shown if it differs.
- **Panel.** Site page, tab PHP: a form with the groups "Limits" and "Behaviour", the default as placeholder, a reset per field.

## Consequences

- Every pool file gets the Hostashi defaults, so existing sites change from the distribution defaults (for example 2M to 64M uploads) on the next render. Hostashi has no production sites yet (web2 still runs CloudPanel), so this is acceptable [A].
- More settings come only per pull request with type, bounds and a test that a hostile value is refused.
- OPcache per site (memory, `validate_timestamps`) is possible because every site has its own master [A], and can follow later with its own entry.
- The import thread and ADR-0017 get the mapping from pool settings.

## Risks to name openly

- **The panel value is not a boundary.** A site can raise `memory_limit` or `max_execution_time` itself; only the slice and nginx stop it. This is intended (option A) and the panel says so.
- **Large uploads use disk.** nginx buffers bodies up to 2G in its temp directory before PHP sees them; the temp directory is outside the site's quota [A]. Bounds stay at 2G for that reason.
- **Long timeouts tie up workers.** A site with 3600 s and few workers can block itself; it does not block other sites (own master) [A].
- **Not a pentest.**

## Open questions

1. ~~Settings as defaults the site may still change (`php_value`, option A), or as fixed values (`php_admin_value`, option B)?~~ **Decided (Kim, 2026-11-21): defaults, option A.**
2. ~~Is the allowlist above right, or is something missing (for example `allow_url_fopen` as a switch, or `output_buffering`)?~~ **Decided (Kim, 2026-11-21): the twelve settings above, nothing more for now.**
3. ~~May the role `user` change all of them for its own site, or only admin and manager the limits (memory, upload, time)?~~ **Decided (Kim, 2026-11-21): `user` may change all of them for its own site.**
