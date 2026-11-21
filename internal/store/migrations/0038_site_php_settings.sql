-- SPDX-License-Identifier: AGPL-3.0-or-later
-- PHP ini settings per site (ADR-0049): the set fields of
-- agentapi.PHPSettings as a JSON object. The panel and the agent check them
-- against the allowlist; a site without a row uses the defaults.

CREATE TABLE site_php_settings (
    site_id    INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    settings   TEXT    NOT NULL CHECK (json_valid(settings) AND json_type(settings) = 'object' AND length(settings) <= 4096),
    -- the user who saved them last; NULL for the importer or a deleted user
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL
) STRICT;
