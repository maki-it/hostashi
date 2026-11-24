-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Resource usage per site (ADR-0063): the 5 min and 1 h tiers of each
-- site's samples, like host_samples (the 15 s tier stays in memory).
-- cpu is percent of one core, mem and cache are bytes (mem is the working
-- set), tasks is a count, io_* are bytes per second and http_* requests
-- and bytes per second. Rows go with the site.

CREATE TABLE site_samples (
    site_id   INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    tier      INTEGER NOT NULL CHECK (tier IN (300, 3600)),
    at        INTEGER NOT NULL,
    cpu       REAL    NOT NULL CHECK (cpu >= 0),
    mem       INTEGER NOT NULL CHECK (mem >= 0),
    cache     INTEGER NOT NULL CHECK (cache >= 0),
    tasks     INTEGER NOT NULL CHECK (tasks >= 0),
    io_read   REAL    NOT NULL CHECK (io_read >= 0),
    io_write  REAL    NOT NULL CHECK (io_write >= 0),
    http_req  REAL    NOT NULL CHECK (http_req >= 0),
    http_in   REAL    NOT NULL CHECK (http_in >= 0),
    http_out  REAL    NOT NULL CHECK (http_out >= 0),
    PRIMARY KEY (site_id, tier, at)
) STRICT, WITHOUT ROWID;

CREATE INDEX site_samples_tier_at ON site_samples (tier, at);
