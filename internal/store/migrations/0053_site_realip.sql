-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Real client address per site (ADR-0059): the set fields of
-- agentapi.WebRealIP as a JSON object (Cloudflare preset, own ranges,
-- header). The panel and the agent check them; a site without a row
-- trusts no proxy.

CREATE TABLE site_realip (
    site_id    INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    realip     TEXT    NOT NULL CHECK (json_valid(realip) AND json_type(realip) = 'object' AND length(realip) <= 16384),
    -- the user who saved it last; NULL for a deleted user
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL
) STRICT;
