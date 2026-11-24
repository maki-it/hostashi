-- SPDX-License-Identifier: AGPL-3.0-or-later
-- Moving TOTP to a new device. The new seed waits here, sealed like
-- totp_secret, while the old one keeps working; the first valid code from
-- the new app swaps it in. Turning TOTP off or resetting the login clears it.

ALTER TABLE users ADD COLUMN totp_pending_secret BLOB;
