-- hostashi: foreign-keys-off
-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The site type python (ADR-0061). SQLite cannot change a CHECK
-- constraint, so sites is rebuilt with SQLite's procedure for a table that
-- other tables reference (https://sqlite.org/lang_altertable.html, "Making
-- Other Kinds Of Table Schema Changes"): the migration runs with foreign
-- keys off, so DROP TABLE deletes no rows of the referencing tables, and
-- PRAGMA foreign_key_check must pass before it commits (migrate.go).
--
-- New: the type python; python_version, the Python line of a Python
-- site's app; the start kinds wsgi and asgi; arguments for Python apps.
-- Everything else is the table as migrations 0001-0055 left it, in the
-- same column order.
--
-- state: creating (row exists, account not confirmed yet), active
-- (account exists, the site is part of the web server state) or deleting
-- (taken out of the web server state, account removal pending).

CREATE TABLE sites_new (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (length(name) BETWEEN 2 AND 32),
    pool TEXT NOT NULL CHECK (length(pool) BETWEEN 1 AND 32),
    state TEXT NOT NULL CHECK (state IN ('creating', 'active', 'deleting')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    php TEXT NOT NULL DEFAULT '' CHECK (php = '' OR php GLOB '[0-9].[0-9]'),
    tls INTEGER NOT NULL DEFAULT 0 CHECK (tls IN (0, 1)),
    tls_domains TEXT NOT NULL DEFAULT '' CHECK (length(tls_domains) <= 13000),
    tls_not_after INTEGER NOT NULL DEFAULT 0,
    tls_error TEXT NOT NULL DEFAULT '' CHECK (length(tls_error) <= 1000),
    tls_failed_at INTEGER NOT NULL DEFAULT 0,
    hsts INTEGER NOT NULL DEFAULT 0 CHECK (hsts IN (0, 1)),
    tls_live INTEGER NOT NULL DEFAULT 0 CHECK (tls_live IN (0, 1)),
    disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    maintenance INTEGER NOT NULL DEFAULT 0 CHECK (maintenance IN (0, 1)),
    maintenance_page INTEGER REFERENCES status_pages (id),
    maintenance_allow TEXT NOT NULL DEFAULT '',
    php_programs INTEGER NOT NULL DEFAULT 0 CHECK (php_programs IN (0, 1)),
    tls_source TEXT NOT NULL DEFAULT 'letsencrypt' CHECK (tls_source IN ('letsencrypt', 'custom')),
    tls_cert_source TEXT NOT NULL DEFAULT '' CHECK (tls_cert_source IN ('', 'letsencrypt', 'custom')),
    tls_trusted INTEGER NOT NULL DEFAULT 1,
    tls_renew_at INTEGER NOT NULL DEFAULT 0,
    tls_renew_forced INTEGER NOT NULL DEFAULT 0,
    disabled_page INTEGER REFERENCES status_pages (id),
    type TEXT NOT NULL DEFAULT 'static' CHECK (type IN ('static', 'php', 'node', 'app', 'proxy', 'redirect', 'python')),
    web_dir TEXT NOT NULL DEFAULT '' CHECK (length(web_dir) <= 255 AND web_dir NOT LIKE '/%' AND web_dir NOT LIKE '%/' AND instr(web_dir, '..') = 0),
    spa INTEGER NOT NULL DEFAULT 0 CHECK (spa IN (0, 1)),
    asset_cache INTEGER NOT NULL DEFAULT 0 CHECK (asset_cache IN (0, 1)),
    asset_cache_seconds INTEGER NOT NULL DEFAULT 0 CHECK (asset_cache_seconds = 0 OR asset_cache_seconds BETWEEN 60 AND 31536000),
    asset_cache_exts TEXT NOT NULL DEFAULT '' CHECK (length(asset_cache_exts) <= 440 AND asset_cache_exts NOT GLOB '*[^a-z0-9 ]*'),
    account INTEGER NOT NULL DEFAULT 1 CHECK (account IN (0, 1)),
    redirect_target TEXT NOT NULL DEFAULT '' CHECK (length(redirect_target) <= 2048 AND (redirect_target = '' OR redirect_target GLOB 'http://?*' OR redirect_target GLOB 'https://?*')),
    redirect_status INTEGER NOT NULL DEFAULT 301 CHECK (redirect_status IN (301, 302, 307, 308)),
    redirect_keep_path INTEGER NOT NULL DEFAULT 0 CHECK (redirect_keep_path IN (0, 1)),
    proxy_target TEXT NOT NULL DEFAULT '' CHECK (length(proxy_target) <= 2048 AND (proxy_target = '' OR proxy_target GLOB 'http://?*' OR proxy_target GLOB 'https://?*')),
    proxy_target_host INTEGER NOT NULL DEFAULT 0 CHECK (proxy_target_host IN (0, 1)),
    proxy_insecure INTEGER NOT NULL DEFAULT 0 CHECK (proxy_insecure IN (0, 1)),
    node_version TEXT NOT NULL DEFAULT '' CHECK (node_version = '' OR node_version GLOB '[1-9][0-9]'),
    app_workdir TEXT NOT NULL DEFAULT 'htdocs' CHECK (length(app_workdir) BETWEEN 1 AND 255),
    app_start_kind TEXT NOT NULL DEFAULT '' CHECK (app_start_kind IN ('', 'script', 'npm', 'binary', 'wsgi', 'asgi')),
    app_start TEXT NOT NULL DEFAULT '' CHECK (length(app_start) <= 255 AND (app_start = '') = (app_start_kind = '')),
    app_port INTEGER CHECK (app_port IS NULL OR app_port BETWEEN 20000 AND 29999),
    app_stopped INTEGER NOT NULL DEFAULT 0 CHECK (app_stopped IN (0, 1)),
    app_args TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(app_args) AND json_type(app_args) = 'array' AND length(app_args) <= 8192 AND (app_args = '[]' OR app_start_kind = 'binary' OR python_version <> '')),
    python_version TEXT NOT NULL DEFAULT '' CHECK (python_version = '' OR python_version GLOB '3.[1-9][0-9]')
) STRICT;

INSERT INTO sites_new (id, name, pool, state, created_at, updated_at, php, tls, tls_domains, tls_not_after, tls_error, tls_failed_at, hsts, tls_live, disabled, maintenance, maintenance_page, maintenance_allow, php_programs, tls_source, tls_cert_source, tls_trusted, tls_renew_at, tls_renew_forced, disabled_page, type, web_dir, spa, asset_cache, asset_cache_seconds, asset_cache_exts, account, redirect_target, redirect_status, redirect_keep_path, proxy_target, proxy_target_host, proxy_insecure, node_version, app_workdir, app_start_kind, app_start, app_port, app_stopped, app_args)
SELECT id, name, pool, state, created_at, updated_at, php, tls, tls_domains, tls_not_after, tls_error, tls_failed_at, hsts, tls_live, disabled, maintenance, maintenance_page, maintenance_allow, php_programs, tls_source, tls_cert_source, tls_trusted, tls_renew_at, tls_renew_forced, disabled_page, type, web_dir, spa, asset_cache, asset_cache_seconds, asset_cache_exts, account, redirect_target, redirect_status, redirect_keep_path, proxy_target, proxy_target_host, proxy_insecure, node_version, app_workdir, app_start_kind, app_start, app_port, app_stopped, app_args
FROM sites;

DROP TABLE sites;

ALTER TABLE sites_new RENAME TO sites;

CREATE UNIQUE INDEX sites_app_port ON sites (app_port) WHERE app_port IS NOT NULL;
