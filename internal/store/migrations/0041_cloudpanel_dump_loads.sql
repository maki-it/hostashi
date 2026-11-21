-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Loading the database dumps of a CloudPanel import (ADR-0030). A row is
-- written when an import with databases starts, so a panel restart resumes
-- the wait for the copy and never loads a dump twice. loads is the list of
-- dumps and the agent job started for each, as JSON. Times are Unix seconds.

CREATE TABLE cloudpanel_dump_loads (
    import_id  TEXT    PRIMARY KEY CHECK (length(import_id) = 16 AND import_id NOT GLOB '*[^0-9a-f]*'),
    state      TEXT    NOT NULL CHECK (state IN ('watching', 'loaded', 'stopped')),
    started_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    loads      TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(loads))
) STRICT;

CREATE INDEX cloudpanel_dump_loads_state ON cloudpanel_dump_loads (state);
