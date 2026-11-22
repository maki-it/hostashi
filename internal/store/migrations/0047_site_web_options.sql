-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Web options of a site (ADR-0054 addendum 1):
-- web_dir: the document root folder below htdocs ('' serves htdocs), for
--   example 'public' or 'dist'. The agent checks it again on every apply.
-- spa: unknown paths get /index.html (single-page apps). Static sites only.
-- asset_cache: Cache-Control on assets. Static sites only.
-- asset_cache_seconds: how long (0: the default of 30 days).
-- asset_cache_exts: the file types, lowercase, separated by spaces
--   ('': the default list of stylesheets, scripts, fonts and images).
-- spa and asset_cache are kept while a site runs PHP, so switching back to
-- static restores them (addendum 3); they are only sent for static sites.

ALTER TABLE sites ADD COLUMN web_dir TEXT NOT NULL DEFAULT ''
    CHECK (length(web_dir) <= 255 AND web_dir NOT LIKE '/%' AND web_dir NOT LIKE '%/' AND instr(web_dir, '..') = 0);
ALTER TABLE sites ADD COLUMN spa INTEGER NOT NULL DEFAULT 0 CHECK (spa IN (0, 1));
ALTER TABLE sites ADD COLUMN asset_cache INTEGER NOT NULL DEFAULT 0 CHECK (asset_cache IN (0, 1));
ALTER TABLE sites ADD COLUMN asset_cache_seconds INTEGER NOT NULL DEFAULT 0
    CHECK (asset_cache_seconds = 0 OR asset_cache_seconds BETWEEN 60 AND 31536000);
ALTER TABLE sites ADD COLUMN asset_cache_exts TEXT NOT NULL DEFAULT ''
    CHECK (length(asset_cache_exts) <= 440 AND asset_cache_exts NOT GLOB '*[^a-z0-9 ]*');
