-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The configuration step of a clone after its files and databases are
-- copied (ADR-0070): '' not started or not needed, running while the
-- agent's WordPress run configures it, done when it finished.
ALTER TABLE site_clones ADD COLUMN config TEXT NOT NULL DEFAULT '' CHECK (config IN ('', 'running', 'done'));
