-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Imports and exports of databases on external servers (ADR-0041 addendum
-- 1). The panel creates a temporary user on the server for each job and
-- drops it once the job has ended; a row lives until then, also across a
-- restart of the panel.

CREATE TABLE db_remote_jobs (
    job_id     TEXT    NOT NULL PRIMARY KEY,
    database   TEXT    NOT NULL,
    server_id  INTEGER NOT NULL REFERENCES db_servers (id) ON DELETE CASCADE,
    db_user    TEXT    NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;
