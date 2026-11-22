-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Security headers per site (ADR-0056): the set fields of
-- agentapi.WebHeaders as a JSON object. The panel and the agent check them;
-- a site without a row sends none.

CREATE TABLE site_headers (
    site_id    INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    headers    TEXT    NOT NULL CHECK (json_valid(headers) AND json_type(headers) = 'object' AND length(headers) <= 16384),
    -- the user who saved them last; NULL for a deleted user
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL
) STRICT;
