-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Optional email address per account (ADR-0044). users.email holds only a
-- confirmed address; a new one waits in email_confirmations until the user
-- opens the link sent to it. Only the SHA-256 of the link's token is stored.
-- Times are Unix seconds.

ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT '' CHECK (length(email) <= 254);

CREATE TABLE email_confirmations (
    user_id    INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    address    TEXT    NOT NULL CHECK (length(address) BETWEEN 3 AND 254),
    token_hash BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL CHECK (expires_at > created_at)
) STRICT;

CREATE INDEX email_confirmations_expires ON email_confirmations (expires_at);
