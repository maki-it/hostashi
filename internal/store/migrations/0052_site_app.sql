-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The app of a Node.js site (ADR-0054); the columns serve the app type
-- (addendum 2) as well, which runs a binary instead of Node.js:
-- node_version: the Node.js LTS line ("24"); '' for none.
-- app_workdir: the working directory, relative to the home.
-- app_start_kind and app_start: how the app starts, one of the typed forms
--   script (a .js/.mjs/.cjs file below the working directory), npm (npm run
--   <script>) or binary (app type); '' until it is set.
-- app_port: the loopback port the panel assigned, 20000-29999, unique.
--   NULL for sites without an app.
-- app_stopped: the app was stopped in the panel and stays stopped.

ALTER TABLE sites ADD COLUMN node_version TEXT NOT NULL DEFAULT ''
    CHECK (node_version = '' OR node_version GLOB '[1-9][0-9]');
ALTER TABLE sites ADD COLUMN app_workdir TEXT NOT NULL DEFAULT 'htdocs'
    CHECK (length(app_workdir) BETWEEN 1 AND 255);
ALTER TABLE sites ADD COLUMN app_start_kind TEXT NOT NULL DEFAULT ''
    CHECK (app_start_kind IN ('', 'script', 'npm', 'binary'));
ALTER TABLE sites ADD COLUMN app_start TEXT NOT NULL DEFAULT ''
    CHECK (length(app_start) <= 255 AND (app_start = '') = (app_start_kind = ''));
ALTER TABLE sites ADD COLUMN app_port INTEGER
    CHECK (app_port IS NULL OR app_port BETWEEN 20000 AND 29999);
ALTER TABLE sites ADD COLUMN app_stopped INTEGER NOT NULL DEFAULT 0 CHECK (app_stopped IN (0, 1));

CREATE UNIQUE INDEX sites_app_port ON sites (app_port) WHERE app_port IS NOT NULL;
