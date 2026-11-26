-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The source of a clone in maintenance while it is copied (ADR-0070): 1
-- while the panel holds the source in maintenance it turned on itself, so
-- it turns off only what it turned on.
ALTER TABLE site_clones ADD COLUMN source_maintenance INTEGER NOT NULL DEFAULT 0 CHECK (source_maintenance IN (0, 1));
