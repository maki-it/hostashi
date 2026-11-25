-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Allow the notification categories 'deploys' (ADR-0062) and 'malware'
-- (ADR-0064) in notification_prefs. Both shipped without widening the
-- CHECK, so saving either switch failed with a constraint error. SQLite
-- cannot alter a CHECK, so the table is rebuilt.

CREATE TABLE notification_prefs_new (
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category TEXT    NOT NULL CHECK (category IN ('backups', 'certificates', 'doctor', 'php', 'disk', 'services', 'deploys', 'malware')),
    enabled  INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    PRIMARY KEY (user_id, category)
) STRICT;

INSERT INTO notification_prefs_new (user_id, category, enabled)
    SELECT user_id, category, enabled FROM notification_prefs;

DROP TABLE notification_prefs;

ALTER TABLE notification_prefs_new RENAME TO notification_prefs;
