-- SPDX-License-Identifier: AGPL-3.0-or-later
-- HSTS per site: whether HTTPS responses carry Strict-Transport-Security.
-- It only takes effect while the site is served over HTTPS.

ALTER TABLE sites ADD COLUMN hsts INTEGER NOT NULL DEFAULT 0 CHECK (hsts IN (0, 1));
