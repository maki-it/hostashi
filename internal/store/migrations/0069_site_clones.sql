-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Clones of a site and their staging mark (ADR-0070). A row exists for
-- every site made by "clone": the site it came from (NULL once that site
-- is deleted), whether it is a staging copy of it, and the state of the
-- last copy of files and databases (the first one or a refresh).
CREATE TABLE site_clones (
    site_id     INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    source_id   INTEGER REFERENCES sites (id) ON DELETE SET NULL,
    staging     INTEGER NOT NULL CHECK (staging IN (0, 1)),
    -- copying: files and databases are being copied
    state       TEXT    NOT NULL CHECK (state IN ('copying', 'done', 'failed')),
    -- refresh: the copy replaces the clone's files and data
    refresh     INTEGER NOT NULL DEFAULT 0 CHECK (refresh IN (0, 1)),
    error       TEXT    NOT NULL DEFAULT '' CHECK (length(error) <= 512),
    -- the agent's home copy job, '' before it started
    home_job    TEXT    NOT NULL DEFAULT '' CHECK (home_job = '' OR (length(home_job) = 16 AND home_job NOT GLOB '*[^0-9a-f]*')),
    home_done   INTEGER NOT NULL DEFAULT 0 CHECK (home_done IN (0, 1)),
    -- the database pairs: [{"source","target","job","done"}]
    databases   TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(databases) AND json_type(databases) = 'array' AND length(databases) <= 16384),
    started_by  INTEGER REFERENCES users (id) ON DELETE SET NULL,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX site_clones_source ON site_clones (source_id);
