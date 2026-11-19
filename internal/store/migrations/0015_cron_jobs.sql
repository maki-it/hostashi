-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Cron jobs per site (ADR-0028): the desired state; every change sends the
-- site's complete list to the agent (cron.apply). Schedule and command are
-- checked in internal/cron before they are stored.

CREATE TABLE cron_jobs (
    id          INTEGER PRIMARY KEY,
    site_id     INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    schedule    TEXT    NOT NULL CHECK (length(schedule) BETWEEN 1 AND 200),
    command     TEXT    NOT NULL CHECK (length(command) BETWEEN 1 AND 4096),
    -- seconds; 0: the default (1 hour)
    timeout_sec INTEGER NOT NULL DEFAULT 0 CHECK (timeout_sec BETWEEN 0 AND 86400),
    -- NULL: the default niceness (ADR-0015)
    nice        INTEGER CHECK (nice BETWEEN 0 AND 19),
    paused      INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;

CREATE INDEX cron_jobs_site ON cron_jobs (site_id, id);
