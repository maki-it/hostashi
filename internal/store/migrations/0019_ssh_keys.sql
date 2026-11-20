-- SPDX-License-Identifier: AGPL-3.0-or-later
-- SSH keys per site (ADR-0032): the desired state; every change sends the
-- site's complete list to the agent (ssh.apply). Keys are checked in
-- internal/sshkeys and stored in canonical form, without options.

CREATE TABLE ssh_keys (
    id          INTEGER PRIMARY KEY,
    site_id     INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    label       TEXT    NOT NULL CHECK (length(label) BETWEEN 1 AND 64),
    access      TEXT    NOT NULL CHECK (access IN ('sftp', 'shell')),
    public_key  TEXT    NOT NULL CHECK (length(public_key) BETWEEN 1 AND 8192),
    fingerprint TEXT    NOT NULL CHECK (length(fingerprint) BETWEEN 1 AND 128),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (site_id, public_key)
) STRICT;

CREATE INDEX ssh_keys_site ON ssh_keys (site_id, id);
