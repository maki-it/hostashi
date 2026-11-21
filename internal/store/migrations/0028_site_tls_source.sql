-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Own certificates per site (ADR-0039). tls_source is where the site's
-- certificate should come from; tls_cert_source is where the live one in
-- tls_domains/tls_not_after came from ('' without one). tls_renew_at is the
-- last "renew now" and tls_renew_forced asks the renewal loop for an ACME
-- order regardless of the expiry.
ALTER TABLE sites ADD COLUMN tls_source TEXT NOT NULL DEFAULT 'letsencrypt'
    CHECK (tls_source IN ('letsencrypt', 'custom'));
ALTER TABLE sites ADD COLUMN tls_cert_source TEXT NOT NULL DEFAULT ''
    CHECK (tls_cert_source IN ('', 'letsencrypt', 'custom'));
ALTER TABLE sites ADD COLUMN tls_trusted INTEGER NOT NULL DEFAULT 1;
ALTER TABLE sites ADD COLUMN tls_renew_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN tls_renew_forced INTEGER NOT NULL DEFAULT 0;
UPDATE sites SET tls_cert_source = 'letsencrypt' WHERE tls_not_after > 0;
