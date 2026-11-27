-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The language of the panel for an account (ADR-0071). 'auto' follows the
-- browser; mails to the account use English for 'auto'.
ALTER TABLE users ADD COLUMN language TEXT NOT NULL DEFAULT 'auto'
    CHECK (language IN ('auto', 'en', 'de'));
