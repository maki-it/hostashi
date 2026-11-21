-- SPDX-License-Identifier: AGPL-3.0-or-later
-- The address an invitation was emailed to (ADR-0044). Accepting the link
-- proves the user reads that mailbox, so the address becomes the account's
-- confirmed address. Empty when the link was only shown to copy.

ALTER TABLE invitations ADD COLUMN email TEXT NOT NULL DEFAULT '' CHECK (length(email) <= 254);
