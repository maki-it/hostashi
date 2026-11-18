-- SPDX-License-Identifier: AGPL-3.0-or-later
-- TOTP second factor. The seed is stored encrypted (see internal/secrets).
-- totp_enabled stays 0 until the user has proven the first code.

ALTER TABLE users ADD COLUMN totp_secret BLOB;
ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0 CHECK (totp_enabled IN (0, 1));
-- Last accepted 30-second step; a code is never accepted twice.
ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;

-- One-time recovery codes, only their SHA-256 is stored.
CREATE TABLE recovery_codes (
    id        INTEGER PRIMARY KEY,
    user_id   INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash BLOB    NOT NULL CHECK (length(code_hash) = 32),
    UNIQUE (user_id, code_hash)
) STRICT;
