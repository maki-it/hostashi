-- SPDX-License-Identifier: AGPL-3.0-or-later
-- One-time invitation links (ADR-0035): a new or reset account sets its own
-- password through a link. Only the SHA-256 of the link's token is stored.
-- Times are Unix seconds. A user holds at most one open invitation.

CREATE TABLE invitations (
    user_id    INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    token_hash BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL CHECK (expires_at > created_at)
) STRICT;

CREATE INDEX invitations_expires ON invitations (expires_at);
