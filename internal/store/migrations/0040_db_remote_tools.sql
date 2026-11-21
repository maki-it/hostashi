-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Sessions of the web database tool on external servers (ADR-0041
-- addendum 1). The panel creates a temporary user on the server for each
-- session and drops it when the session ends; a row lives until then, also
-- across a restart of the panel.

CREATE TABLE db_remote_tools (
    db_user    TEXT    NOT NULL PRIMARY KEY,
    database   TEXT    NOT NULL,
    server_id  INTEGER NOT NULL REFERENCES db_servers (id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
) STRICT;
