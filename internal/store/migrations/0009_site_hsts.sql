-- SPDX-License-Identifier: AGPL-3.0-or-later
-- HSTS per site, and whether a site is served over HTTPS right now.
-- HSTS: whether HTTPS responses carry Strict-Transport-Security.
-- It only takes effect while the site is served over HTTPS.

ALTER TABLE sites ADD COLUMN hsts INTEGER NOT NULL DEFAULT 0 CHECK (hsts IN (0, 1));
-- tls_live: the last web.apply the agent accepted served the site over
-- HTTPS. The API reports this, not what the certificate would allow.
ALTER TABLE sites ADD COLUMN tls_live INTEGER NOT NULL DEFAULT 0 CHECK (tls_live IN (0, 1));
