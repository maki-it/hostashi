-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Initial schema: panel users and the audit log.
-- Credentials (password hash, TOTP, passkeys) follow with the login feature.

CREATE TABLE users (
    id         INTEGER PRIMARY KEY,
    username   TEXT    NOT NULL UNIQUE CHECK (length(username) BETWEEN 1 AND 64),
    disabled   INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
) STRICT;

CREATE TABLE audit_log (
    id      INTEGER PRIMARY KEY,
    at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    user_id INTEGER REFERENCES users (id) ON DELETE SET NULL,
    action  TEXT    NOT NULL CHECK (length(action) BETWEEN 1 AND 128),
    target  TEXT    NOT NULL DEFAULT '',
    detail  TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(detail))
) STRICT;

CREATE INDEX audit_log_at ON audit_log (at);
