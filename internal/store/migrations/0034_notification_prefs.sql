-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Operations email switches per account (ADR-0044). A missing row means the
-- default of the role: on for admin, off for the others.

CREATE TABLE notification_prefs (
    user_id  INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category TEXT    NOT NULL CHECK (category IN ('backups', 'certificates', 'doctor', 'php', 'disk')),
    enabled  INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    PRIMARY KEY (user_id, category)
) STRICT;
