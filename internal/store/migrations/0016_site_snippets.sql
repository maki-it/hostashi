-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Custom nginx snippets per site (ADR-0029), one per slot. The agent checks
-- them against its allowlist before they are stored and again on every
-- web.apply. A site without a row has no snippets.

CREATE TABLE site_snippets (
    site_id    INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    server     TEXT    NOT NULL DEFAULT '' CHECK (length(server) <= 65536),
    root       TEXT    NOT NULL DEFAULT '' CHECK (length(root) <= 65536),
    -- the user who saved them last; NULL for the importer or a deleted user
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL
) STRICT;
