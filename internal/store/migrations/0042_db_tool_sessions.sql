-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Sessions of the web database tool, local and on external servers
-- (ADR-0038 addendum 1): who opened one and when it ends, so the panel can
-- list, extend and end them. The agent (local) and db_remote_tools
-- (external) say whether a session still runs; a row outlives it until
-- the panel next looks. Times are Unix seconds.

CREATE TABLE db_tool_sessions (
    db_user    TEXT    NOT NULL PRIMARY KEY CHECK (db_user GLOB 'hostashi_tool_*'),
    database   TEXT    NOT NULL,
    remote     INTEGER NOT NULL DEFAULT 0 CHECK (remote IN (0, 1)),
    opened_by  INTEGER REFERENCES users (id) ON DELETE SET NULL,
    opened_at  INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX db_tool_sessions_database ON db_tool_sessions (database);
