-- SPDX-License-Identifier: AGPL-3.0-or-later
-- A token can be limited to some sites (ADR-0018, ADR-0035). site_limited
-- is separate from the list: when every listed site is deleted, the token
-- reaches no site, never every site.
ALTER TABLE api_tokens ADD COLUMN site_limited INTEGER NOT NULL DEFAULT 0
    CHECK (site_limited IN (0, 1));

CREATE TABLE api_token_sites (
    token_id INTEGER NOT NULL REFERENCES api_tokens (id) ON DELETE CASCADE,
    site_id  INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    PRIMARY KEY (token_id, site_id)
) STRICT;

CREATE INDEX api_token_sites_site ON api_token_sites (site_id);
