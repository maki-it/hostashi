-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Database limits of a site (ADR-0057): database_bytes is the warning
-- threshold for the size of the site's local databases (no enforcement),
-- database_connections the MAX_USER_CONNECTIONS of each of its database
-- users. 0 means unlimited, as in the other limit columns.
ALTER TABLE limit_presets ADD COLUMN database_bytes INTEGER NOT NULL DEFAULT 0 CHECK (database_bytes >= 0);
ALTER TABLE limit_presets ADD COLUMN database_connections INTEGER NOT NULL DEFAULT 0 CHECK (database_connections >= 0);
ALTER TABLE site_limits ADD COLUMN database_bytes INTEGER NOT NULL DEFAULT 0 CHECK (database_bytes >= 0);
ALTER TABLE site_limits ADD COLUMN database_connections INTEGER NOT NULL DEFAULT 0 CHECK (database_connections >= 0);

-- The seeded presets get the values of ADR-0057, if an admin kept their
-- names; renamed or own presets stay unlimited.
UPDATE limit_presets SET database_bytes = 2147483648, database_connections = 20 WHERE name = 'small';
UPDATE limit_presets SET database_bytes = 5368709120, database_connections = 40 WHERE name = 'medium';
UPDATE limit_presets SET database_bytes = 10737418240, database_connections = 80 WHERE name = 'large';

-- Sites that follow a preset take its values; the panel applies them on
-- its next start (applied_at = 0).
UPDATE site_limits SET
    database_bytes = (SELECT p.database_bytes FROM limit_presets p WHERE p.id = site_limits.preset_id),
    database_connections = (SELECT p.database_connections FROM limit_presets p WHERE p.id = site_limits.preset_id),
    applied_at = 0
WHERE preset_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM limit_presets p WHERE p.id = site_limits.preset_id AND (p.database_bytes > 0 OR p.database_connections > 0));
