-- SPDX-License-Identifier: AGPL-3.0-or-later
-- How the panel looks for an account (ADR-0037). color_scheme 'system'
-- follows the browser; theme names the token set, only 'default' so far.
ALTER TABLE users ADD COLUMN color_scheme TEXT NOT NULL DEFAULT 'system'
    CHECK (color_scheme IN ('system', 'light', 'dark'));
ALTER TABLE users ADD COLUMN theme TEXT NOT NULL DEFAULT 'default'
    CHECK (theme IN ('default'));
