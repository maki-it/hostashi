-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Parked domains (ADR-0027): a domain without a site that shows a parking
-- page with status 200, over HTTPS with its own certificate. A domain is
-- either parked or belongs to a site; adding it to a site removes the
-- parking (store.insertDomains).

CREATE TABLE parked_domains (
    id            INTEGER PRIMARY KEY,
    domain        TEXT    NOT NULL UNIQUE CHECK (length(domain) BETWEEN 3 AND 253),
    -- NULL: the built-in parking page
    page          INTEGER REFERENCES status_pages (id),
    tls           INTEGER NOT NULL DEFAULT 1 CHECK (tls IN (0, 1)),
    -- the last accepted web state serves the domain over HTTPS
    tls_live      INTEGER NOT NULL DEFAULT 0 CHECK (tls_live IN (0, 1)),
    tls_not_after INTEGER NOT NULL DEFAULT 0,
    tls_error     TEXT    NOT NULL DEFAULT '',
    tls_failed_at INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
) STRICT;
