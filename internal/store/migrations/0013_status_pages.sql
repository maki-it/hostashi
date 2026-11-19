-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Maintenance and parking pages (ADR-0027). The panel database is the
-- desired state; the agent gets the files on demand. The built-in pages are
-- part of the program, not of this table.

CREATE TABLE status_pages (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    kind       TEXT    NOT NULL CHECK (kind IN ('maintenance', 'parking')),
    -- agentapi.PageDigest of the files
    digest     TEXT    NOT NULL CHECK (length(digest) = 64),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE status_page_files (
    page_id INTEGER NOT NULL REFERENCES status_pages (id) ON DELETE CASCADE,
    path    TEXT    NOT NULL CHECK (length(path) BETWEEN 1 AND 200),
    content BLOB    NOT NULL,
    PRIMARY KEY (page_id, path)
) STRICT;

-- Maintenance of a site: on/off, the page (NULL: the built-in one) and the
-- allowed addresses (canonical IPs or CIDRs, separated by spaces). A page
-- that a site names cannot be deleted, even while maintenance is off.
ALTER TABLE sites ADD COLUMN maintenance INTEGER NOT NULL DEFAULT 0 CHECK (maintenance IN (0, 1));
ALTER TABLE sites ADD COLUMN maintenance_page INTEGER REFERENCES status_pages (id);
ALTER TABLE sites ADD COLUMN maintenance_allow TEXT NOT NULL DEFAULT '';
