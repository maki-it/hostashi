-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The environment variables of the app of a Node.js or app site (ADR-0054).
-- value is sealed with the panel's secret key (internal/secrets), bound to
-- the site and the name, so a row cannot be moved to another site or
-- variable. The panel checks names and values before it seals them.

CREATE TABLE site_app_env (
    site_id INTEGER NOT NULL REFERENCES sites (id) ON DELETE CASCADE,
    name    TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 64 AND name GLOB '[A-Z_]*' AND name NOT GLOB '*[^A-Z0-9_]*'),
    value   BLOB    NOT NULL CHECK (length(value) <= 8192),
    PRIMARY KEY (site_id, name)
) STRICT;
