-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Service management and the server dashboard (ADR-0052).
--
-- service_events: what the panel noticed about the units of the service
-- catalog (a crash restart systemd did, a start, a failed state) and the
-- actions admins ran. Kept 30 days and at most 10,000 rows. Times are Unix
-- seconds.
--
-- host_samples: the 5 min and 1 h tiers of the dashboard's ring buffer
-- (the 15 s tier stays in memory). disks and net are JSON arrays the panel
-- writes itself: [{"name":"root","used":1,"total":2}] and
-- [{"name":"eth0","rx":1.5,"tx":2.5}] (bytes and bytes per second).

CREATE TABLE service_events (
    id       INTEGER NOT NULL PRIMARY KEY,
    at       INTEGER NOT NULL,
    service  TEXT    NOT NULL CHECK (service GLOB '[a-z]*' AND length(service) <= 32),
    site     TEXT    NOT NULL DEFAULT '' CHECK (length(site) <= 32),
    kind     TEXT    NOT NULL CHECK (kind IN ('crash-restart', 'started', 'failed', 'action')),
    -- the action (start, stop, restart, reload) for kind 'action', the
    -- systemd result (exit-code, signal, ...) for 'failed', else empty
    detail   TEXT    NOT NULL DEFAULT '' CHECK (length(detail) <= 32),
    -- automatic restarts this event stands for (crash-restart only)
    restarts INTEGER NOT NULL DEFAULT 0 CHECK (restarts >= 0),
    user_id  INTEGER REFERENCES users (id) ON DELETE SET NULL
) STRICT;

CREATE INDEX service_events_at ON service_events (at);
CREATE INDEX service_events_service ON service_events (service, site, at);

CREATE TABLE host_samples (
    tier        INTEGER NOT NULL CHECK (tier IN (300, 3600)),
    at          INTEGER NOT NULL,
    cpu_busy    REAL    NOT NULL,
    cpu_iowait  REAL    NOT NULL,
    cpu_steal   REAL    NOT NULL,
    load1       REAL    NOT NULL,
    mem_used    INTEGER NOT NULL,
    mem_total   INTEGER NOT NULL,
    swap_used   INTEGER NOT NULL,
    swap_total  INTEGER NOT NULL,
    disks       TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(disks)),
    net         TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(net)),
    PRIMARY KEY (tier, at)
) STRICT, WITHOUT ROWID;
