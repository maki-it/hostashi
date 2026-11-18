-- SPDX-License-Identifier: AGPL-3.0-or-later
-- API tokens (ADR-0018) and a small key/value table for panel settings.
-- Only the SHA-256 of a token is stored, never the token. Times are Unix seconds.

CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    -- First characters of the secret part, so the owner can recognize a token.
    prefix       TEXT    NOT NULL CHECK (length(prefix) BETWEEN 1 AND 16),
    token_hash   BLOB    NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    scopes       TEXT    NOT NULL CHECK (json_valid(scopes) AND json_type(scopes) = 'array'),
    created_at   INTEGER NOT NULL,
    -- NULL means the token never expires (allowed on purpose, see ADR-0018).
    expires_at   INTEGER,
    last_used_at INTEGER,
    last_used_ip TEXT    NOT NULL DEFAULT '',
    revoked_at   INTEGER
) STRICT;

CREATE INDEX api_tokens_user ON api_tokens (user_id);

-- Panel settings. The key is chosen by the code, never by a request.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY CHECK (length(key) BETWEEN 1 AND 64),
    value TEXT NOT NULL CHECK (json_valid(value))
) STRICT;
