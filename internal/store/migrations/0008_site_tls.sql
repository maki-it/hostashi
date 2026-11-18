-- SPDX-License-Identifier: AGPL-3.0-or-later
-- HTTPS per site. tls is what the admin wants; the other columns describe
-- the live certificate (the key stays with the agent) and the last failed
-- attempt to get one. Times are Unix seconds, 0 for none.

ALTER TABLE sites ADD COLUMN tls INTEGER NOT NULL DEFAULT 0 CHECK (tls IN (0, 1));
-- space-separated names of the live certificate
ALTER TABLE sites ADD COLUMN tls_domains TEXT NOT NULL DEFAULT '' CHECK (length(tls_domains) <= 13000);
ALTER TABLE sites ADD COLUMN tls_not_after INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN tls_error TEXT NOT NULL DEFAULT '' CHECK (length(tls_error) <= 1000);
ALTER TABLE sites ADD COLUMN tls_failed_at INTEGER NOT NULL DEFAULT 0;
