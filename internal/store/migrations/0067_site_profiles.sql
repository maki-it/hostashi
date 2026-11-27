-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Application profile of a site (ADR-0069) and the hardening values the
-- site changed against it (ADR-0017 Part 4), as a JSON object of only the
-- changed values. A site without a row follows the default profile of its
-- type (static or generic PHP) without changes.
CREATE TABLE site_profiles (
    site_id    INTEGER PRIMARY KEY REFERENCES sites (id) ON DELETE CASCADE,
    profile    TEXT NOT NULL CHECK (length(profile) BETWEEN 1 AND 64),
    overrides  TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(overrides) AND json_type(overrides) = 'object' AND length(overrides) <= 16384),
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL
) STRICT;
