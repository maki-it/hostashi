-- SPDX-License-Identifier: AGPL-3.0-or-later
-- DNS cut-over of a migrated site (ADR-0031). While a cut-over waits, the
-- panel resolves the site's domains once a minute; when all of them point
-- to this server it turns on HTTPS. answers is the last result per domain
-- as JSON. One row per site: starting again replaces a finished one.

CREATE TABLE site_cutovers (
    site_id    INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    state      TEXT    NOT NULL CHECK (state IN ('waiting', 'switched', 'failed', 'canceled')),
    started_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    started_at INTEGER NOT NULL,
    deadline   INTEGER NOT NULL,
    checked_at INTEGER NOT NULL DEFAULT 0,
    done_at    INTEGER NOT NULL DEFAULT 0,
    answers    TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(answers)),
    error      TEXT    NOT NULL DEFAULT ''
) STRICT;
