-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Uploaded CloudPanel scan reports (ADR-0017, ADR-0026). A full report
-- carries custom nginx snippets and cron commands that may hold passwords,
-- so it is stored sealed with the panel key, bound to its id. The plan is
-- computed again on every read, against the sites of this moment.

CREATE TABLE cloudpanel_reports (
    id         TEXT    PRIMARY KEY CHECK (length(id) = 32),
    sites      INTEGER NOT NULL CHECK (sites >= 0),
    sealed     BLOB    NOT NULL,
    created_by INTEGER REFERENCES users (id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL
) STRICT;
