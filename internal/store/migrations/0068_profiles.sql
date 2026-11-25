-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Own application profiles (ADR-0069): an admin's copy of a built-in
-- profile with changed values. data is the typed profile as JSON, never
-- nginx text; the panel checks it before every write. A site refers to a
-- profile by name in site_profiles, so a profile that a site uses cannot
-- be deleted.
CREATE TABLE profiles (
    name       TEXT PRIMARY KEY CHECK (length(name) BETWEEN 1 AND 40),
    data       TEXT NOT NULL CHECK (json_valid(data) AND json_type(data) = 'object' AND length(data) <= 65536),
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    updated_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
