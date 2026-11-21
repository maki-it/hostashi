-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The object cache of a preset (ADR-0045): 0 for off, else the size in MiB.
-- It is the starting value of a site that gets the preset; later changes
-- of the preset only change the size of following sites whose cache is on.
ALTER TABLE limit_presets ADD COLUMN object_cache_mb INTEGER NOT NULL DEFAULT 0
    CHECK (object_cache_mb = 0 OR object_cache_mb BETWEEN 32 AND 1024);
