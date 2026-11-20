-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Whether a site's PHP may start programs (exec, system, ...); off by
-- default, the pool then disables those functions (ADR-0033).

ALTER TABLE sites ADD COLUMN php_programs INTEGER NOT NULL DEFAULT 0 CHECK (php_programs IN (0, 1));
