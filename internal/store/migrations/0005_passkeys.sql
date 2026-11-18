-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Passkeys (WebAuthn). Each user has a random user handle that the
-- authenticator stores; it is not the numeric user id. A user may hold
-- several passkeys. The credential column is the public part only.

CREATE TABLE webauthn_handles (
    user_id INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    handle  BLOB    NOT NULL UNIQUE CHECK (length(handle) = 32)
) STRICT;

CREATE TABLE passkeys (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id BLOB    NOT NULL UNIQUE CHECK (length(credential_id) BETWEEN 1 AND 1023),
    name          TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    -- Public key, sign counter and flags as JSON (go-webauthn Credential).
    credential    TEXT    NOT NULL CHECK (json_valid(credential)),
    created_at    INTEGER NOT NULL,
    last_used_at  INTEGER
) STRICT;

CREATE INDEX passkeys_user ON passkeys (user_id);
