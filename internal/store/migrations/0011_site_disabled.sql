-- SPDX-License-Identifier: AGPL-3.0-or-later
-- A disabled site keeps its account, files, databases and certificate, but
-- the web server answers its domains with 503 and its PHP pool is stopped.
-- It is separate from state, which tracks creating and deleting.

ALTER TABLE sites ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1));
