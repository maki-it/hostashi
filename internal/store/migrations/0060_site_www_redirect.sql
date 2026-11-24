-- SPDX-License-Identifier: AGPL-3.0-or-later
-- www redirect per site (ADR-0065): '' serves every domain, 'to-www'
-- sends d to www.d and 'to-non-www' sends www.d to d, for each pair of
-- the site's domains. A redirect site keeps the value but does not use it.

ALTER TABLE sites ADD COLUMN www_redirect TEXT NOT NULL DEFAULT ''
    CHECK (www_redirect IN ('', 'to-www', 'to-non-www'));
