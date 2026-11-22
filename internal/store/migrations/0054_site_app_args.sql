-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The arguments of the program of an app site (ADR-0054 addendum 2), as a
-- JSON array of strings; '[]' for every other start command. The panel and
-- the agent check them (at most 16, each at most 256 bytes, no NUL or line
-- break); the agent quotes each one for systemd.

ALTER TABLE sites ADD COLUMN app_args TEXT NOT NULL DEFAULT '[]'
    CHECK (json_valid(app_args) AND json_type(app_args) = 'array' AND length(app_args) <= 8192
        AND (app_args = '[]' OR app_start_kind = 'binary'));
