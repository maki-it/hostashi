-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Database servers (ADR-0041). Row 1 is the local server, reached by the
-- agent over its socket; it cannot be removed and is the default until
-- another server is marked. External servers are reached over TCP by the
-- panel process with an administrator whose password is sealed with the
-- server key (purpose "db-server:<id>"). engine, version, client_host,
-- tls_active and last_error are what the last check saw.

CREATE TABLE db_servers (
    id             INTEGER PRIMARY KEY,
    name           TEXT    NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 32),
    host           TEXT    NOT NULL,
    port           INTEGER NOT NULL CHECK (port BETWEEN 0 AND 65535),
    admin_user     TEXT    NOT NULL,
    admin_password BLOB,
    tls_mode       TEXT    NOT NULL CHECK (tls_mode IN ('socket', 'verify', 'verify-ca', 'none')),
    ca_pem         TEXT    NOT NULL DEFAULT '',
    client_host    TEXT    NOT NULL DEFAULT '',
    engine         TEXT    NOT NULL DEFAULT '',
    version        TEXT    NOT NULL DEFAULT '',
    tls_active     INTEGER NOT NULL DEFAULT 0 CHECK (tls_active IN (0, 1)),
    last_error     TEXT    NOT NULL DEFAULT '',
    checked_at     INTEGER NOT NULL DEFAULT 0,
    is_default     INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
    created_at     INTEGER NOT NULL,
    CHECK ((id = 1) = (tls_mode = 'socket')),
    CHECK (id = 1 OR admin_password IS NOT NULL)
) STRICT;

-- At most one default; the store keeps exactly one.
CREATE UNIQUE INDEX db_servers_default ON db_servers (is_default) WHERE is_default = 1;

INSERT INTO db_servers (id, name, host, port, admin_user, tls_mode, is_default, created_at)
VALUES (1, 'localhost', '', 0, '', 'socket', 1, 0);

-- ADD COLUMN cannot carry a REFERENCES clause with a non-NULL default, so
-- triggers keep databases.server_id pointing at a server.
ALTER TABLE databases ADD COLUMN server_id INTEGER NOT NULL DEFAULT 1;

CREATE INDEX databases_server ON databases (server_id);

CREATE TRIGGER databases_server_insert BEFORE INSERT ON databases
WHEN NOT EXISTS (SELECT 1 FROM db_servers WHERE id = NEW.server_id)
BEGIN
    SELECT RAISE(ABORT, 'unknown database server');
END;

CREATE TRIGGER databases_server_update BEFORE UPDATE OF server_id ON databases
BEGIN
    SELECT RAISE(ABORT, 'a database cannot move to another server');
END;

CREATE TRIGGER db_servers_delete BEFORE DELETE ON db_servers
WHEN OLD.id = 1 OR EXISTS (SELECT 1 FROM databases WHERE server_id = OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'database server is in use');
END;
