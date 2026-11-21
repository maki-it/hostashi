-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The page cache of a preset (ADR-0046): 0 for off, else the size in MB.
-- It is the starting value of a site that gets the preset; later changes
-- of the preset only change the size of following sites whose cache is on.
ALTER TABLE limit_presets ADD COLUMN page_cache_mb INTEGER NOT NULL DEFAULT 0
    CHECK (page_cache_mb IN (0, 64, 128, 256, 512, 1024, 2048, 4096));
