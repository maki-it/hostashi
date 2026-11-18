-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Password login: credentials on users, server-side sessions, append-only audit log.
-- TOTP and passkeys follow in their own migrations.

ALTER TABLE users ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin'));

-- Only the SHA-256 of the session token is stored, never the token itself.
-- Times are Unix seconds.
CREATE TABLE sessions (
    id         INTEGER PRIMARY KEY,
    token_hash BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_expires ON sessions (expires_at);

-- The audit log is append-only. Changing user_id is still allowed, because
-- deleting a user sets it to NULL (ON DELETE SET NULL).
CREATE TRIGGER audit_log_no_update
BEFORE UPDATE OF id, at, action, target, detail ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;

CREATE TRIGGER audit_log_no_delete
BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;
