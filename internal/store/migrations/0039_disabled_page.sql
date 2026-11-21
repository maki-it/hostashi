-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The page a disabled site shows (ADR-0047): a maintenance page, NULL for
-- the built-in one. Like maintenance_page, a page that a site names cannot
-- be deleted, even while the site is enabled.
ALTER TABLE sites ADD COLUMN disabled_page INTEGER REFERENCES status_pages (id);
