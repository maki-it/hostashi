-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Display name of a site (ADR-0072). The site name stays the system name
-- (Linux user, home folder, API path); the title is display text only and
-- never reaches the agent. Empty means no display name. length() counts
-- characters; the panel also refuses control and format characters.

ALTER TABLE sites ADD COLUMN title TEXT NOT NULL DEFAULT '' CHECK (length(title) <= 64);
