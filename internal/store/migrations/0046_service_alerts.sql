-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Email notifications for service outages (ADR-0055).
--
-- service_events gets the kind 'recovered': a unit that was failed is
-- active again. notification_prefs gets the category 'services'. SQLite
-- cannot change a CHECK constraint, so both tables are rebuilt with their
-- rows; nothing references either table.

CREATE TABLE service_events_new (
    id       INTEGER NOT NULL PRIMARY KEY,
    at       INTEGER NOT NULL,
    service  TEXT    NOT NULL CHECK (service GLOB '[a-z]*' AND length(service) <= 32),
    site     TEXT    NOT NULL DEFAULT '' CHECK (length(site) <= 32),
    kind     TEXT    NOT NULL CHECK (kind IN ('crash-restart', 'started', 'failed', 'action', 'recovered')),
    -- the action (start, stop, restart, reload) for kind 'action', the
    -- systemd result (exit-code, signal, ...) for 'failed', else empty
    detail   TEXT    NOT NULL DEFAULT '' CHECK (length(detail) <= 32),
    -- automatic restarts this event stands for (crash-restart only)
    restarts INTEGER NOT NULL DEFAULT 0 CHECK (restarts >= 0),
    user_id  INTEGER REFERENCES users (id) ON DELETE SET NULL
) STRICT;

INSERT INTO service_events_new (id, at, service, site, kind, detail, restarts, user_id)
    SELECT id, at, service, site, kind, detail, restarts, user_id FROM service_events;

DROP TABLE service_events;

ALTER TABLE service_events_new RENAME TO service_events;

CREATE INDEX service_events_at ON service_events (at);
CREATE INDEX service_events_service ON service_events (service, site, at);

CREATE TABLE notification_prefs_new (
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category TEXT    NOT NULL CHECK (category IN ('backups', 'certificates', 'doctor', 'php', 'disk', 'services')),
    enabled  INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    PRIMARY KEY (user_id, category)
) STRICT;

INSERT INTO notification_prefs_new (user_id, category, enabled)
    SELECT user_id, category, enabled FROM notification_prefs;

DROP TABLE notification_prefs;

ALTER TABLE notification_prefs_new RENAME TO notification_prefs;
