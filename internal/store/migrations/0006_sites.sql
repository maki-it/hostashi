-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Sites and their domains: the desired state the panel hands to the agent.
-- The name is also the site's Linux user (validated in agentapi). Times are
-- Unix seconds.

CREATE TABLE sites (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE CHECK (length(name) BETWEEN 2 AND 32),
    pool       TEXT    NOT NULL CHECK (length(pool) BETWEEN 1 AND 32),
    -- creating: row exists, account not confirmed yet
    -- active:   account exists, the site is part of the web server state
    -- deleting: taken out of the web server state, account removal pending
    state      TEXT    NOT NULL CHECK (state IN ('creating', 'active', 'deleting')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE site_domains (
    domain   TEXT    PRIMARY KEY CHECK (length(domain) BETWEEN 3 AND 253),
    site_id  INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    -- 0 is the primary domain
    position INTEGER NOT NULL CHECK (position >= 0),
    UNIQUE (site_id, position)
) STRICT;

CREATE INDEX site_domains_site ON site_domains (site_id, position);
