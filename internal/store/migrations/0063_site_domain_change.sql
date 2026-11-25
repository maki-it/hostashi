-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Change the domain of a site (ADR-0068).
--
-- site_domains.redirect marks a redirect domain: it stays on the site and
-- its certificate and answers with 301 to the primary domain. The served
-- domains come first; position 0 is always the primary domain, which never
-- redirects.
--
-- site_domain_changes holds a domain change that waits for its
-- certificate: the new served and redirect domains as JSON arrays, who
-- asked, and the last failure. Its names are reserved: no other site,
-- parked domain or change may take them (checked by the panel).

ALTER TABLE site_domains ADD COLUMN redirect INTEGER NOT NULL DEFAULT 0
    CHECK (redirect IN (0, 1) AND NOT (redirect = 1 AND position = 0));

CREATE TABLE site_domain_changes (
    site_id          INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    domains          TEXT    NOT NULL CHECK (json_valid(domains) AND json_type(domains) = 'array' AND json_array_length(domains) >= 1),
    redirect_domains TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(redirect_domains) AND json_type(redirect_domains) = 'array'),
    -- the user who asked; NULL for a deleted user
    requested_by     INTEGER REFERENCES users (id) ON DELETE SET NULL,
    requested_at     INTEGER NOT NULL,
    error            TEXT    NOT NULL DEFAULT '' CHECK (length(error) <= 4096),
    failed_at        INTEGER NOT NULL DEFAULT 0
) STRICT;
