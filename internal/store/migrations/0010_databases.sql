-- SPDX-License-Identifier: AGPL-3.0-or-later
-- MariaDB databases and their users (ADR-0024). Names are validated in
-- agentapi; they are unique on the server, so they are unique here too.
-- A site cannot be removed while it has databases: the panel drops them
-- through the agent first. No password is stored.

CREATE TABLE databases (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 64),
    site_id    INTEGER NOT NULL REFERENCES sites (id),
    created_at INTEGER NOT NULL
) STRICT;

CREATE INDEX databases_site ON databases (site_id);

CREATE TABLE database_users (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 32),
    database_id INTEGER NOT NULL REFERENCES databases (id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL
) STRICT;

CREATE INDEX database_users_database ON database_users (database_id);
