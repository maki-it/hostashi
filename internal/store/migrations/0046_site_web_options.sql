-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Web options of a site (ADR-0054 addendum 1):
-- web_dir: the document root folder below htdocs ('' serves htdocs), for
--   example 'public' or 'dist'. The agent checks it again on every apply.
-- spa: unknown paths get /index.html (single-page apps). Static sites only.
-- asset_cache: 30 days of Cache-Control on assets. Static sites only.
-- spa and asset_cache are kept while a site runs PHP, so switching back to
-- static restores them (addendum 3); they are only sent for static sites.

ALTER TABLE sites ADD COLUMN web_dir TEXT NOT NULL DEFAULT ''
    CHECK (length(web_dir) <= 255 AND web_dir NOT LIKE '/%' AND web_dir NOT LIKE '%/' AND instr(web_dir, '..') = 0);
ALTER TABLE sites ADD COLUMN spa INTEGER NOT NULL DEFAULT 0 CHECK (spa IN (0, 1));
ALTER TABLE sites ADD COLUMN asset_cache INTEGER NOT NULL DEFAULT 0 CHECK (asset_cache IN (0, 1));
