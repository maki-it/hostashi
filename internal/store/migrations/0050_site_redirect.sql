-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Redirect sites (ADR-0054):
-- account: the site has a Linux account and home. A redirect site is
--   created without one; a site that becomes a redirect later keeps its
--   account (addendum 3).
-- redirect_target: the http(s) URL every address goes to; '' unless the
--   site is or was a redirect. Kept after a type change, so switching
--   back restores it.
-- redirect_status: 301, 302, 307 or 308.
-- redirect_keep_path: append the request's path and query to the target.

ALTER TABLE sites ADD COLUMN account INTEGER NOT NULL DEFAULT 1 CHECK (account IN (0, 1));
ALTER TABLE sites ADD COLUMN redirect_target TEXT NOT NULL DEFAULT ''
    CHECK (length(redirect_target) <= 2048 AND (redirect_target = '' OR redirect_target GLOB 'http://?*' OR redirect_target GLOB 'https://?*'));
ALTER TABLE sites ADD COLUMN redirect_status INTEGER NOT NULL DEFAULT 301 CHECK (redirect_status IN (301, 302, 307, 308));
ALTER TABLE sites ADD COLUMN redirect_keep_path INTEGER NOT NULL DEFAULT 0 CHECK (redirect_keep_path IN (0, 1));
