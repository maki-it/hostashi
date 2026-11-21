# ADR-0046 Page cache (nginx FastCGI cache) per site

**Status:** Accepted (Kim, 2026-11-21). **Date drafted:** 2026-11-21. **Decides:** Kim. **Related:** ADR-0004 (web/agent privilege split), ADR-0005 (web server), ADR-0015 (per-site limits and quota), ADR-0016 (storage pools), ADR-0017 (import and presets), ADR-0026 (CloudPanel transfer), ADR-0027 (maintenance pages), ADR-0029 (nginx snippets), ADR-0033 (hardening baseline), ADR-0035 (roles), ADR-0037 (UI redesign), ADR-0045 (object cache per site). **Changes in part:** ADR-0017 (the Varnish option for release 1.0), see open question 1.

Evidence tags as in [README](README.md): [V] verified, [A] assumption, [E] estimate, [U] unverified general knowledge.

## Context

- WordPress is the main workload, and a page cache is a core hosting feature [V: ADR-0005, S3]. `03-features.md` 3.9 lists "Nginx FastCGI cache with purge" (P1, Beta) and "Varnish integration" (P1, release 1.0) [V].
- ADR-0017 keeps a Varnish option for release 1.0, decided by Kim on 2026-11-18 together with the importer [V: ADR-0017, "Owner input"]. On 2026-11-20 Kim asked for a comparison and agreed to draft this ADR for the FastCGI cache **instead of** Varnish [V: Kim, project chat, 2026-11-20].
- Decided by Kim on 2026-11-20: the page cache (this ADR) and the object cache (ADR-0045) both ship in v1 [V: Kim, project chat]. The FastCGI cache replaces Varnish for now, so Varnish leaves the v1 scope [V: Kim, thread, 2026-11-21].
- Spike S3 generated a working FastCGI cache config for nginx, with `X-Cache: MISS` then `HIT`, and about 77,500 requests per second on a cache hit [V: `spike/s3-webserver/REPORT.md`]. It is part of stock nginx; no extra module or service is needed [V: S3].
- Hostashi renders one `nginx.conf` with an `http` block and one server block per site from Go templates in `internal/web` [V: `internal/web/nginx.go`]. `fastcgi_cache_path` is only valid in the `http` block [U: nginx documentation], so each site's cache zone is rendered there.
- Every site has its own Linux user and a project quota on its directory tree; project quota counts files by project ID, whatever their owner [V: ADR-0015, `internal/agent/quota.go`].
- nginx workers run as `www-data`. Site homes belong to the site user with group `www-data` [A: from the reconciler design in `internal/web`, to recheck during implementation].
- On web2, one site has a cache port (`6081`) between the TLS front block and the PHP backend block, probably Varnish [A: `spike/s6-adopt/REPORT.md`]. The S6 scan does not look at Varnish itself [V: S6 report, "not discovered"]. Kim confirmed on 2026-11-21 that one site runs Varnish and uses CloudPanel's WordPress plugin to purge the cache when content changes [V: Kim, thread]. No other Varnish features are known.
- The CloudPanel WordPress purge plugin `clp-wp-varnish-cache` is GPLv3; Hostashi does not copy it [V: ADR-0001, ADR-0002].

## Options

### Engine

| Option | Pro | Contra |
|--------|-----|--------|
| **A. nginx FastCGI cache per site** | Built into the nginx we already run, no extra service, no extra attack surface; sits where TLS ends; directory, size and purge cleanly per site; no own config language | No tag purge; purge of a single URL needs our own code or a third-party module; cache on disk (page cache of the kernel keeps hot pages in RAM [U]) |
| B. Varnish per server | Fine-grained purge (bans, tags), VCL, RAM cache, many CMS plugins send purges | nginx needed in front (TLS) and behind (PHP), so two hops and a second config per site; one shared memory pool for all sites unless one instance per site; VCL is a second language to render safely; one more service and package to harden |
| C. Both: FastCGI cache by default, Varnish as an option | Covers big shops | Two cache designs to test and support in v1 |

### Where the cache files live

| Option | Pro | Contra |
|--------|-----|--------|
| **A. A directory per site outside the site home, owned by `www-data`, mode `0700`, tagged with the site's project ID** | The site user cannot plant symlinks or files that nginx later writes through or serves; counts against the site's disk quota | Must be on the same filesystem as the site (project IDs are per filesystem) |
| B. Inside the site home | Quota "just works" | The site user controls the directory: symlink tricks against a `www-data` writer, cache poisoning by writing fake entries [A] |
| C. One shared directory or tmpfs | Simple | No per-site size, purge or quota |

## Recommendation

**Engine A, location A**, opt-in per site, off by default.

### nginx config

- **Zone per site** in the `http` block:
  `fastcgi_cache_path <pool>/.hostashi-cache/<site> levels=1:2 keys_zone=hst_<site>:2m max_size=<size> inactive=1d use_temp_path=off;`
  `2m` holds about 16,000 keys [U: nginx documentation, 1 MB about 8,000 keys]. `use_temp_path=off` keeps temporary files in the same directory, so they count against the same quota and need no copy between filesystems.
- **In the PHP location** of the site:
  - `fastcgi_cache hst_<site>;`
  - `fastcgi_cache_key "$scheme$request_method$host$request_uri";` (fixed, never user input; also the basis for a later single-URL purge).
  - `fastcgi_cache_methods GET HEAD;`
  - `fastcgi_cache_valid 200 301 302 <ttl>;` and `fastcgi_cache_valid 404 1m;` Never other status codes.
  - `fastcgi_cache_lock on;`, `fastcgi_cache_use_stale error timeout updating http_500 http_503;`, `fastcgi_cache_background_update on;` (one request regenerates, the rest get the stale copy).
  - `fastcgi_cache_bypass $hst_skip_<site>;` and `fastcgi_no_cache $hst_skip_<site>;`
  - Upstream headers are **honoured**: no `fastcgi_ignore_headers`. A response with `Set-Cookie`, `Cache-Control: private`, `no-store` or `no-cache`, or `X-Accel-Expires: 0` is not cached [U: nginx default behaviour, to verify in the container test].
  - `X-Cache: $upstream_cache_status` (`HIT`, `MISS`, `BYPASS`, `EXPIRED`, `STALE`, `UPDATING`). It is rendered at the same level as the site's other `add_header` lines, because an `add_header` in a deeper block drops the inherited ones [U: nginx inheritance rule; the generator test checks that security headers survive].
- **Skip rules** (`$hst_skip_<site>`, from `map` blocks rendered by Go, no `if` in locations). Always on, cannot be switched off:
  - any method other than `GET` and `HEAD`;
  - an `Authorization` header;
  - the preset's cookies (below);
  - the preset's fixed paths and the site's own excluded paths.
- **Presets per site type** (data in Go, chosen in the panel; ADR-0017 site types):

  | Preset | Skip on cookie (name prefix) | Skip on path or query |
  |--------|------------------------------|------------------------|
  | WordPress | `wordpress_logged_in_`, `wp-postpass_`, `comment_author_`, `wordpress_sec_` | `/wp-admin/`, `/wp-login.php`, `/wp-cron.php`, `/xmlrpc.php`, `/wp-json/`, `preview=true` |
  | WooCommerce | WordPress plus `woocommerce_items_in_cart`, `woocommerce_cart_hash`, `wp_woocommerce_session_` | WordPress plus `/cart/`, `/checkout/`, `/my-account/`, `add-to-cart=`, `wc-ajax=` [A: default page slugs; the excluded paths let users add translated slugs] |
  | Generic PHP | **any** `Cookie` header | none |

  The generic preset is strict on purpose: without knowing the app, a cookie may mean a session [A]. Apps that set analytics cookies only get fewer hits, never someone else's page.
- **Excluded paths** per site: up to 50 entries, each a path prefix with an optional trailing `*`, checked against `^/[A-Za-z0-9._~%/-]{0,200}\*?$`. Go escapes each entry (`regexp.QuoteMeta`) before it goes into the `map`; quotes, `;`, braces, `$`, spaces and control characters cannot pass the check. Table-driven tests cover traversal (`/../`), encoded characters, empty input and over-long input. The rendered config goes through the existing `nginx -t` generation switch (S2).
- **Cache time** from fixed choices: 1, 5, 10, 30 and 60 minutes, default 10 [E].
- **Size** from fixed choices: 64 MB to 4 GB, default 256 MB [E]. `max_size` is enforced by the nginx cache manager, which can overshoot briefly [U]; the project quota is the hard backstop. If the quota is full, writing a cache entry fails and the page is served uncached [U: to verify, must not become a 5xx].
- Sites in maintenance mode (ADR-0027) are served by nginx without PHP, so nothing is cached then.

### Cache directory and agent

- `web.apply` (the existing reconciler) creates `<pool>/.hostashi-cache/<site>` as `www-data:www-data` `0700` with the site's project ID and inherit flag, on the same pool as the site (ADR-0016). `<pool>/.hostashi-cache` is `root:root` `0711`. When project quota is not available, only `max_size` limits the size and doctor says so.
- New typed agent operation **`pagecache.purge(site)`**: opens the site's cache directory with `openat2` and `RESOLVE_NO_SYMLINKS`, renames it to a trash name, recreates an empty one with the same owner, mode and project ID, and deletes the trash in the background. nginx treats an entry whose file is gone as a miss [U: to verify in the container test, must not log 5xx]. Pure Go, **no new `os/exec`**, no nginx module.
- Purge runs automatically when the cache is switched off, on site delete, after a restore (ADR-0008) and when maintenance mode ends.
- **Purge by API in v1** (decided by Kim on 2026-11-21): `POST /api/v1/sites/{name}/page-cache/purge` calls `pagecache.purge`. A new scope `pagecache:purge` that does nothing else, so a token for a CMS can hold only this scope, limited to one site through the token's site list (ADR-0035). Tokens never grant more than their owner's role. The route is rate limited per site (for example one purge per 10 seconds, extra calls answer `429`) [E], so a save loop in WordPress cannot keep the cache empty or keep the agent busy. The route goes into the route matrix test like every other route. This replaces the CloudPanel purge plugin on the migrated site; until Hostashi has its own WordPress plugin (after v1), any plugin or hook that can send an HTTP request on save can call it [A].
- Single-URL purge is **not** in v1, but the fixed key makes it possible later in Go: the file name is the MD5 of the key in the `levels=1:2` layout [U]. That is the basis for auto-purge from WordPress after v1.

### Panel and roles

- A section "Page cache" on the site page (ADR-0037): switch, preset, cache time, excluded paths, size, "Purge cache", and a hint how to check `X-Cache`.
- Roles as in ADR-0045 (ADR-0035): a `user` on granted sites may switch the cache on and off, purge it, pick preset and cache time and edit excluded paths. Only admin and manager set the size, because it counts against the site's disk limit. Limit presets (ADR-0015) get a field "page cache: off or size", default off.
- API (ADR-0018): the same fields under the site resource, scopes as for the object cache.

### Doctor and metrics

- doctor checks that every enabled cache directory exists with the right owner, mode and project ID, and that no cache directory belongs to a site that has none.
- Metrics (ADR-0022): cache size per site from the agent. Hit rate per site from `$upstream_cache_status` in the site access logs is later work [A].

### Backups

- The cache directory is outside the site home and is not backed up; restic excludes `.hostashi-cache` anyway.

### CloudPanel import

- The scanner already sees the two-tier vhosts and the cache port in between [V: S6]. A site with a cache tier gets "page cache: on" pre-selected, with the preset from its site type, and a review note: the Varnish VCL is not migrated, and a Varnish purge plugin in WordPress should be removed after the move because it has nothing to talk to.
- Cache contents are not migrated. A cache starts empty; that is safe by definition.

## Consequences

- Varnish leaves the v1 scope (Kim, 2026-11-21); `03-features.md` 3.9 and ADR-0017 get a note. Varnish can come back after v1 as its own ADR if a big shop needs tag purge.
- Cached pages count against the site's disk quota, so a large cache leaves less room for files. The panel says so next to the size.
- One `keys_zone` of 2 MB shared memory per site with the cache on; at 22 sites less than 50 MB [E].
- The container tests on Debian 13 and Ubuntu 26.04: `MISS` then `HIT`; `BYPASS` for a logged-in WordPress cookie, a POST, `/wp-admin/` and an excluded path; no cache for a `Set-Cookie` response; purge leads to `MISS` without errors; a site user cannot write into or list the cache directory; security headers are still present on cached answers.

## Risks to name openly

- **Personal data in the cache.** If an app shows personal content without a cookie or `Cache-Control: private` (for example a token in the URL), a visitor can get another visitor's page. The fixed skip rules and the strict generic preset reduce this; the app's own headers stay decisive. The panel shows this warning when the cache is switched on.
- **Cache poisoning through unkeyed headers.** If an app changes its output based on a request header that is not in the key (for example `X-Forwarded-Host` or `Accept-Language`), one crafted request can poison the cache for everyone. Hostashi's key is fixed; such apps need the cache off or paths excluded [A].
- **Stale pages.** Without auto-purge in v1, edits show up after the cache time at the latest. This is why the default is 10 minutes and the panel has a purge button.
- **Not a pentest.**

## Implementation notes

Added while building it (2026-11-21):

- **No purge after a restore.** A restore (ADR-0008) writes into a separate restore directory, never over the live web root, so the cache does not serve pages of files that changed under it. The admin who moves restored files into place can purge by hand.
- **Switching off removes the directory** instead of purging it. Every `web.apply` removes each cache directory of a site that has no cache, also one left over by an earlier failure; doctor reports leftovers until then (`system.pagecache`).
- **Purge also runs** when the preset, the cache time or the excluded paths change, and when a site serves again after maintenance or being disabled. A size change alone keeps the entries.
- **Rate limit** of the purge route: one purge per site every 10 seconds, kept in memory; `429` with `Retry-After`.
- **Import:** the plan proposes the preset from the site type (WordPress or generic PHP) for a PHP site with a cache tier; the admin can untick it when creating the site.
- **Limit presets** (Kim, 2026-11-21): the field "page cache: off or size" is a default for new sites, as for the object cache (ADR-0045). A site gets it when it is created, imported or switched to the preset; a site that never chose a cache preset gets the strict generic one. A later change of the preset only changes the size of following sites whose cache is on; the preset never switches a cache on or off.
- **Metrics** for the cache size per site are not in this first version.

## Open questions

1. ~~Should the FastCGI cache **replace** the Varnish option from ADR-0017 in v1?~~ Decided by Kim on 2026-11-21: yes, for now. Varnish possibly after v1 as its own ADR.
2. ~~Which site on web2 runs behind the cache port `6081`?~~ Answered by Kim on 2026-11-21: one WordPress site, which uses CloudPanel's purge plugin. Decided by Kim on 2026-11-21: v1 gets a purge API call with a token limited to that one action and site; an own WordPress plugin follows after v1.
3. ~~Presets, skip rules, cache times and sizes as above?~~ Confirmed by Kim on 2026-11-21.
4. ~~Roles as above, the same split as for the object cache?~~ Confirmed by Kim on 2026-11-21.
