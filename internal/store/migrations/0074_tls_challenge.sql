-- SPDX-License-Identifier: AGPL-3.0-or-later
-- How Let's Encrypt checks the domains of a site (ADR-0076): 'http' is
-- HTTP-01 over port 80, 'dns' is DNS-01 with a DNS account (0073). The
-- panel address keeps its choice in its settings entry.
ALTER TABLE sites ADD COLUMN tls_challenge TEXT NOT NULL DEFAULT 'http'
    CHECK (tls_challenge IN ('http', 'dns'));
