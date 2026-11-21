-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The page cache (nginx FastCGI cache) of a site (ADR-0046). A row keeps
-- the settings while the cache is off. exclude is a JSON array of paths.
CREATE TABLE site_page_cache (
    site_id    INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    enabled    INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    preset     TEXT NOT NULL CHECK (preset IN ('wordpress', 'woocommerce', 'php')),
    minutes    INTEGER NOT NULL CHECK (minutes IN (1, 5, 10, 30, 60)),
    size_mb    INTEGER NOT NULL CHECK (size_mb IN (64, 128, 256, 512, 1024, 2048, 4096)),
    exclude    TEXT NOT NULL DEFAULT '[]',
    updated_at INTEGER NOT NULL
) STRICT;
