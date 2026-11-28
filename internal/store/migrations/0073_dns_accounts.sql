-- SPDX-License-Identifier: AGPL-3.0-or-later
-- DNS accounts for DNS-01 certificates (ADR-0076). credentials is the JSON
-- of the provider's fields, sealed with the server key (purpose
-- "dns-account:<id>"); it never leaves the panel. zones is a JSON array of
-- the zone names the account may write. last_error and checked_at are what
-- the last access check saw.
CREATE TABLE dns_accounts (
    id          INTEGER PRIMARY KEY,
    label       TEXT    NOT NULL UNIQUE CHECK (length(label) BETWEEN 1 AND 64),
    provider    TEXT    NOT NULL CHECK (provider IN ('inwx', 'hetzner', 'cloudflare')),
    credentials BLOB    NOT NULL,
    zones       TEXT    NOT NULL DEFAULT '[]',
    last_error  TEXT    NOT NULL DEFAULT '',
    checked_at  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;
