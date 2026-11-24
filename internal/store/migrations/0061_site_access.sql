-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Password protection and IP rules per site (ADR-0066). site_access holds
-- the IP rule, the password switch, the protected paths and the bypass
-- addresses as a JSON object; no row lets everybody in. site_auth_users
-- holds the basic auth users with their bcrypt hashes, never a password.
-- The panel and the agent check every value.

CREATE TABLE site_access (
    site_id    INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    settings   TEXT    NOT NULL CHECK (json_valid(settings) AND json_type(settings) = 'object' AND length(settings) <= 65536),
    -- the user who saved them last; NULL for a deleted user
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE site_auth_users (
    site_id    INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 64 AND name NOT GLOB '*[^A-Za-z0-9._@-]*'),
    hash       TEXT    NOT NULL CHECK (length(hash) = 60 AND hash GLOB '$2[aby]$[0-9][0-9]$*'),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    PRIMARY KEY (site_id, name)
) STRICT;
