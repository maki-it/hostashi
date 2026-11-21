-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Own certificates for parked domains (ADR-0039), as for sites in 0026.
ALTER TABLE parked_domains ADD COLUMN tls_source TEXT NOT NULL DEFAULT 'letsencrypt'
    CHECK (tls_source IN ('letsencrypt', 'custom'));
ALTER TABLE parked_domains ADD COLUMN tls_cert_source TEXT NOT NULL DEFAULT ''
    CHECK (tls_cert_source IN ('', 'letsencrypt', 'custom'));
ALTER TABLE parked_domains ADD COLUMN tls_trusted INTEGER NOT NULL DEFAULT 1;
ALTER TABLE parked_domains ADD COLUMN tls_renew_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE parked_domains ADD COLUMN tls_renew_forced INTEGER NOT NULL DEFAULT 0;
UPDATE parked_domains SET tls_cert_source = 'letsencrypt' WHERE tls_not_after > 0;
