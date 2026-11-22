-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The type of a site (ADR-0054): static, php, node, app, proxy or redirect.
-- Until now the type followed from php alone (a version: PHP, empty:
-- static); existing sites keep exactly that. The type can be changed later
-- (ADR-0054 addendum 3), so it is a plain column, not part of the key.

ALTER TABLE sites ADD COLUMN type TEXT NOT NULL DEFAULT 'static'
    CHECK (type IN ('static', 'php', 'node', 'app', 'proxy', 'redirect'));

UPDATE sites SET type = 'php' WHERE php <> '';
