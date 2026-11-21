-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The object cache of a site (ADR-0045). A site without a row has no cache.
-- memory_mb stays when the cache is turned off, so turning it on again
-- (which a user may do) uses the size an admin or manager set.
CREATE TABLE site_object_cache (
    site_id    INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    enabled    INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    memory_mb  INTEGER NOT NULL CHECK (memory_mb BETWEEN 32 AND 1024),
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL
) STRICT;
